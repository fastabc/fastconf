package fastconf

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/watcher"
)

type dirReloadSlot struct {
	mu      sync.Mutex
	running bool
	pending bool
	reason  string
}

// startFileWatcher subscribes parent directories before loading their contents,
// so file creation and Kubernetes symlink swaps cannot slip between scan and watch.
func (m *Manager[T]) startFileWatcher(ctx context.Context) error {
	if m.opts.FS != nil {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "watch: skipped (virtual fs in use)")
		return nil
	}
	paths := staticWatchPaths(&m.opts)
	if len(paths) == 0 {
		return nil
	}
	co := coalesce.New(m.opts.CoalesceWindows, func(key, reason string) {
		select {
		case <-m.lifetime.Done():
			return
		default:
		}
		if m.eventsPaused.Load() {
			m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "watch: event ignored (paused)", slog.String("key", key), slog.String("reason", reason))
			return
		}
		entry, _ := m.dirReloadSlots.LoadOrStore(key, &dirReloadSlot{})
		slot := entry.(*dirReloadSlot)
		slot.mu.Lock()
		slot.reason = reason
		if slot.running {
			slot.pending = true
			slot.mu.Unlock()
			return
		}
		slot.running = true
		for {
			reason = slot.reason
			slot.pending = false
			slot.mu.Unlock()
			if err := m.requestReload(context.Background(), reason, key); err != nil {
				m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "watch: reload failed", slog.String("key", key), slog.String("reason", reason), slog.Any("err", err))
			}
			slot.mu.Lock()
			if !slot.pending {
				slot.running = false
				slot.mu.Unlock()
				return
			}
		}
	})
	w, err := watcher.New(paths, co)
	if err != nil {
		return err
	}
	m.fileWatcher = w
	m.bgWG.Add(1)
	go func() {
		defer m.bgWG.Done()
		defer co.Stop()
		defer func() { _ = w.Close() }()
		w.Run(ctx)
	}()
	m.opts.Logger.LogAttrs(context.Background(), slog.LevelInfo, "watch: started", slog.Any("paths", paths))
	return nil
}

// watchSourceDir runs before discovery reads a directory or its metadata.
// The watcher deduplicates paths and tracks directory replacement.
func (m *Manager[T]) watchSourceDir(dir string) {
	if m.fileWatcher == nil {
		return
	}
	select {
	case <-m.lifetime.Done():
		return
	default:
	}
	if dir = normalizeWatchPath(dir); dir == "" {
		return
	}
	if err := m.fileWatcher.AddPath(dir); err != nil {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "watch: add source dir failed", slog.String("path", dir), slog.Any("err", err))
	}
}

func staticWatchPaths(o *options) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	// Watch root and conventional subtrees for mounts at either directory level.
	add(o.Dir)
	add(filepath.Join(o.Dir, "base"))
	add(filepath.Join(o.Dir, "overlays"))
	for _, p := range o.WatchPaths {
		add(p)
	}
	for _, e := range o.Providers {
		for _, path := range e.Info.WatchPaths {
			add(path)
		}
	}
	return out
}

func normalizeWatchPath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// Pause makes the manager ignore file-watcher and provider change events
// until Resume. It discards a pending overflow refresh but does not cancel
// queued or in-flight requests. Explicit Reload, Plan and History().Rollback
// still run. After Resume, explicitly Reload to catch changes made while paused.
// Notifier refreshes return ErrPaused so consumers retain unacknowledged events;
// periodic resync skips paused intervals.
func (m *Manager[T]) Pause() {
	if m != nil {
		m.eventsPaused.Store(true)
		select {
		case <-m.overflowResyncCh:
		default:
		}
	}
}

// Resume re-enables event-driven reloads after Pause.
func (m *Manager[T]) Resume() {
	if m != nil {
		m.eventsPaused.Store(false)
	}
}

// Paused reports whether event-driven reloads are paused.
func (m *Manager[T]) Paused() bool { return m != nil && m.eventsPaused.Load() }
