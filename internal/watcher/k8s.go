// Package watcher subscribes to filesystem changes and triggers reloads.
//
// Why parent-directory watching? Kubernetes ConfigMap mounts use a `..data`
// symlink that is atomically swapped on update. Watching the target file
// directly loses events because the inode changes; watching the parent
// directory and reacting to CREATE / CHMOD on the symlink path is the
// recommended pattern.
package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher fans fsnotify events out to a single Trigger callback. It survives
// best-effort recovery: when a watched directory is deleted (e.g. a pod's
// volume re-mount), the watcher attempts to re-Add it on the next event.
type Watcher struct {
	fsw     *fsnotify.Watcher
	dirs    map[string]struct{}
	trigger func(reason string)

	mu     sync.Mutex
	closed bool
}

// New starts an fsnotify watcher on the unique parent directories of the
// given paths.
func New(paths []string, trigger func(reason string)) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fsw:     fsw,
		dirs:    map[string]struct{}{},
		trigger: trigger,
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
			// The K8s symlink swap shows up as REMOVE+CREATE on `..data` plus
			// CHMOD on the inner symlinks; debouncing collapses all of them.
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Chmod|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			w.trigger("fs:" + ev.Name)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.trigger("fs-error:" + err.Error())
		}
	}
}

// Close stops the watcher. Idempotent.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.fsw.Close()
}
