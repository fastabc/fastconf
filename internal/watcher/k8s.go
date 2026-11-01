// Package watcher sends parent-directory events to a coalescer. Watching parents
// survives Kubernetes symlink inode replacement; ..data renames trigger SwapHint.
// See docs/cookbook/deployment/k8s.md.
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

// Watcher feeds fsnotify events to a parent-keyed coalescer. On directory replacement
// it watches the nearest existing ancestor until the target can be registered again.
type Watcher struct {
	fsw *fsnotify.Watcher
	co  *coalesce.Coalescer

	mu     sync.Mutex
	want   map[string]struct{} // every path requested through AddPath
	dirs   map[string]struct{} // directories currently registered with fsw
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
		want: map[string]struct{}{},
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

// AddPath watches an existing directory non-recursively, otherwise its nearest
// existing ancestor. It is idempotent and a no-op after Close.
func (w *Watcher) AddPath(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.want[path] = struct{}{}
	_, err := w.register(path)
	return err
}

// register watches path's directory and reports whether a new watch was added.
func (w *Watcher) register(path string) (bool, error) {
	dir := watchDir(path)
	if _, ok := w.dirs[dir]; ok {
		return false, nil
	}
	if err := w.fsw.Add(dir); err != nil {
		return false, err
	}
	w.dirs[dir] = struct{}{}
	return true, nil
}

// watchDir returns path when it is a directory, otherwise its nearest
// existing ancestor.
func watchDir(path string) string {
	dir := path
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// track keeps registrations in step with directory lifecycle events: a
// removed or renamed directory loses its (now dead) watch, and every
// requested path is re-resolved so it is watched directly again once it
// exists, or through its nearest ancestor until then.
func (w *Watcher) track(ev fsnotify.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	switch {
	case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		if _, ok := w.dirs[ev.Name]; !ok {
			return
		}
		_ = w.fsw.Remove(ev.Name)
		delete(w.dirs, ev.Name)
	case ev.Op&fsnotify.Create != 0:
		if fi, err := os.Stat(ev.Name); err != nil || !fi.IsDir() {
			return
		}
	default:
		return
	}
	// Repeat while watches are added: `mkdir -p` may create nested
	// directories before the watch on their parent is in place.
	for added := true; added; {
		added = false
		for path := range w.want {
			if ok, _ := w.register(path); ok {
				added = true
			}
		}
	}
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
			// Re-register before the reload this event triggers.
			w.track(ev)
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

// isK8sSwapCommit recognizes CREATE/RENAME of ..data or transient ..data_tmp_*
// entries, treating either as a signal to shorten the coalescing window.
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
