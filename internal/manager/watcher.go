package manager

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/coalesce"
	iopts "github.com/fastabc/fastconf/internal/options"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/internal/watcher"
)

// startWatcher arms the fsnotify-backed watcher when WithWatch(true) is set.
//
// fsnotify operates on real filesystems only — when the manager runs on an
// in-memory fs.FS (testing/fstest), startWatcher silently no-ops because
// there is nothing to observe.
func (m *M[T]) startWatcher(ctx context.Context) error {
	if m.opts.FS != nil {
		m.opts.Log.Debug().Msg("watch: skipped (virtual fs in use)")
		return nil
	}
	paths := collectWatchPaths(m.opts)
	// Also watch directories that contain the active layer files.
	// After the first Load(), m.state holds the real sources; these may live
	// in profile-specific or axis-specific sub-directories that the static
	// scan above misses.
	for _, p := range collectWatchPathsFromState(m.state.Load()) {
		paths = appendUnique(paths, p)
	}
	if len(paths) == 0 {
		return nil
	}
	co := coalesce.New(m.opts.Coalesce, func(key, reason string) {
		select {
		case <-m.closed:
			return
		default:
		}
		if m.watchPaused.Load() {
			m.opts.Log.Debug().Str("key", key).Str("reason", reason).Msg("watch: event ignored (paused)")
			return
		}
		if err := m.requestReloadWithKey(context.Background(), reason, key); err != nil {
			m.opts.Log.Warn().Str("key", key).Str("reason", reason).Err(err).Msg("watch: reload failed")
		}
	})
	w, err := watcher.New(paths, co)
	if err != nil {
		return err
	}
	m.bgWG.Add(1)
	go func() {
		defer m.bgWG.Done()
		defer co.Stop()
		defer func() { _ = w.Close() }()
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-m.closed:
				cancel()
			case <-runCtx.Done():
			}
		}()
		w.Run(runCtx)
	}()
	m.opts.Log.Info().Strs("paths", paths).Msg("watch: started")
	return nil
}

func collectWatchPaths(o iopts.Options) []string {
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
	add(o.Dir)
	add(filepath.Join(o.Dir, "base"))
	add(filepath.Join(o.Dir, "overlays"))
	for _, p := range o.WatchPaths {
		add(p)
	}
	for _, p := range o.Providers {
		wp, ok := p.(contracts.WatchPathProvider)
		if !ok {
			continue
		}
		for _, path := range wp.WatchPaths() {
			add(path)
		}
	}
	return out
}

// collectWatchPathsFromState returns the unique parent directories of every
// active layer in s.Sources. These are the directories that actually contribute
// to the loaded config and must be watched for hot-reload to fire on profile-
// or axis-specific overlay changes.
func collectWatchPathsFromState[T any](s *istate.State[T]) []string {
	if s == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, src := range s.Sources {
		// Skip virtual sources (bytes://, provider://, ...).
		if src.Path == "" || strings.Contains(src.Path, "://") {
			continue
		}
		dir := filepath.Dir(src.Path)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	return out
}

// appendUnique appends p to dst only if it is not already present.
func appendUnique(dst []string, p string) []string {
	if slices.Contains(dst, p) {
		return dst
	}
	return append(dst, p)
}
