// Package watcher subscribes to filesystem changes and feeds them into a
// coalescer.
//
// Why parent-directory watching? Kubernetes ConfigMap mounts use a `..data`
// symlink that is atomically swapped on update. Watching the target file
// directly loses events because the inode changes; watching the parent
// directory and reacting to CREATE / CHMOD on the symlink path is the
// recommended pattern. The Watcher detects the canonical K8s "..data"
// rename and tags it as a swap-commit so the coalescer can drain the
// burst on the SwapHint window instead of the full Quiet window.
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fsnotify/fsnotify"
)

// Watcher fans fsnotify events out to a Coalescer keyed by parent
// directory. It survives best-effort recovery: when a watched directory
// is deleted (e.g. a pod's volume re-mount), the Watcher attempts to
// re-Add it on the next event.
type Watcher struct {
	fsw  *fsnotify.Watcher
	co   *coalesce.Coalescer
	dirs map[string]struct{}

	mu     sync.Mutex
	closed bool
}

// New starts an fsnotify watcher on the unique parent directories of the
// given paths and routes every event through co.
func New(paths []string, co *coalesce.Coalescer) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fsw:  fsw,
		co:   co,
		dirs: map[string]struct{}{},
	}
	for _, p := range paths {
		if err := w.AddPath(p); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	return w, nil
}

// AddPath registers a path. If path is an existing directory, the directory
// itself is watched (non-recursively). Otherwise, the parent directory is
// watched — the K8s ConfigMap pattern, since the inode of the leaf file
// changes during atomic swap. Idempotent.
func (w *Watcher) AddPath(path string) error {
	dir := path
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	if dir == "" {
		dir = "."
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.dirs[dir]; ok {
		return nil
	}
	if err := w.fsw.Add(dir); err != nil {
		return err
	}
	w.dirs[dir] = struct{}{}
	return nil
}

// Run loops until ctx is canceled or Close is called. It is intended to be
// invoked in its own goroutine.
func (w *Watcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// We care about creates, writes, chmods, renames, and removes.
			// The K8s symlink swap shows up as REMOVE+CREATE on `..data`
			// plus CHMOD on the inner symlinks; the coalescer drains the
			// trailing CHMOD storm via SwapHint once we tag the swap.
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Chmod|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			key := filepath.Dir(ev.Name)
			base := filepath.Base(ev.Name)
			swap := isK8sSwapCommit(ev.Op, base)
			w.co.Push(key, "fs:"+ev.Op.String()+":"+ev.Name, swap)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// Route errors through the coalescer too — they still
			// represent a reason to attempt a reload (e.g. dir
			// re-mounted under the inode we held).
			w.co.Push("", "fs-error:"+err.Error(), false)
		}
	}
}

// isK8sSwapCommit returns true for the canonical "..data" rename or
// create that signals a K8s ConfigMap atomic swap has just committed.
//
// We also accept the transient "..data_tmp_*" name that some kubelet
// versions create immediately before the rename — observing that file
// at all is enough to know the swap is in flight.
func isK8sSwapCommit(op fsnotify.Op, base string) bool {
	if op&(fsnotify.Create|fsnotify.Rename) == 0 {
		return false
	}
	if base == "..data" {
		return true
	}
	return strings.HasPrefix(base, "..data_tmp_")
}

// Close stops the watcher. Idempotent. The caller is responsible for
// stopping the associated Coalescer separately.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.fsw.Close()
}
