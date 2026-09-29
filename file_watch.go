package fastconf

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"

	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/internal/watcher"
)

// startWatcher arms the fsnotify-backed watcher when WithWatch(Watch{}) is set.
//
// fsnotify operates on real filesystems only — when the manager runs on an
// in-memory fs.FS (testing/fstest), startWatcher silently no-ops because
// there is nothing to observe.
func (m *Manager[T]) startWatcher(ctx context.Context) error {
	if m.opts.FS != nil {
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "watch: skipped (virtual fs in use)")
		return nil
	}
	paths := collectWatchPaths(&m.opts)
	// Also watch every directory the initial reload scanned: profile- and
	// axis-specific overlays that the static list above misses. The reload
	// loop has not served a request yet, so scannedDirs is stable here.
	for _, p := range m.scannedDirs {
		if p = normalizeWatchPath(p); !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	co := coalesce.New(m.opts.Coalesce, func(key, reason string) {
		select {
		case <-m.lifetime.Done():
			return
		default:
		}
		if m.watchPaused.Load() {
			m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "watch: event ignored (paused)", slog.String("key", key), slog.String("reason", reason))
			return
		}
		if err := m.requestReload(context.Background(), reason, key); err != nil {
			m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "watch: reload failed", slog.String("key", key), slog.String("reason", reason), slog.Any("err", err))
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

// refreshWatchPaths subscribes to every directory a reload scanned. The
// watcher deduplicates, and re-resolves the paths when directories are
// removed and recreated.
func (m *Manager[T]) refreshWatchPaths(dirs []string) {
	m.scannedDirs = dirs
	if m.fileWatcher == nil {
		return
	}
	select {
	case <-m.lifetime.Done():
		return
	default:
	}
	for _, p := range dirs {
		if p = normalizeWatchPath(p); p == "" {
			continue
		}
		if err := m.fileWatcher.AddPath(p); err != nil {
			m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "watch: add source dir failed", slog.String("path", p), slog.Any("err", err))
		}
	}
}

func collectWatchPaths(o *options) []string {
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
	// Watch the configured root and the conventional base/overlay subtrees so
	// that K8s ConfigMap parent-dir swaps reach us regardless of whether the
	// user mounted the bundle at `conf.d` or at one level deeper.
	// NOTE: base and overlays are added unconditionally. Deployments that use
	// a flat conf.d layout (no subdirectories) will see ENOENT here, which the
	// underlying fsnotify implementation silently swallows — no action needed.
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
// until Resume. Explicit Reload, Plan and History().Rollback still run.
func (m *Manager[T]) Pause() {
	if m != nil {
		m.watchPaused.Store(true)
	}
}

// Resume re-enables event-driven reloads after Pause.
func (m *Manager[T]) Resume() {
	if m != nil {
		m.watchPaused.Store(false)
	}
}

// Paused reports whether event-driven reloads are paused.
func (m *Manager[T]) Paused() bool { return m != nil && m.watchPaused.Load() }
