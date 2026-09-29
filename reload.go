package fastconf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// Reload entry point and pipeline orchestration. reload_queue.go serializes
// later requests; New and Load run the initial pipeline synchronously.

// Reload triggers a synchronous reload. On failure the previous state
// is preserved.
//
// Options:
//   - WithOverride(map) injects a one-shot in-memory layer at the
//     top of the priority stack for this reload only. The map is copied
//     while Reload applies options; do not mutate it concurrently with
//     the Reload call.
//   - WithReason(s) overrides the default "manual" reason tag used
//     for audit / metrics / logging.
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error {
	if m == nil {
		return ErrClosed
	}
	cfg := reloadConfig{reason: "manual"}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.err != nil {
		// Option errors fail on the reload goroutine like any other reload,
		// so the Errors() channel keeps a single writer.
		optErr := cfg.err
		return m.enqueue(ctx, reloadRequest{
			ctx:     ctx,
			reason:  overrideReloadReason(cfg.reason),
			applyFn: func(context.Context) error { return optErr },
		})
	}
	if cfg.override == nil {
		return m.requestReload(ctx, cfg.reason, "")
	}
	extra := stagedLayer{
		src: SourceRef{
			Path:     "override://once",
			Kind:     LayerOverride,
			Priority: bandOverride,
		},
		class: classOverride,
		data:  cfg.override,
	}
	reason := overrideReloadReason(cfg.reason)
	return m.enqueue(ctx, reloadRequest{
		ctx:    ctx,
		reason: reason,
		applyFn: func(pipeCtx context.Context) error {
			return m.reload(pipeCtx, reason, "", extra)
		},
	})
}

// ReloadOption tunes a single Reload invocation.
type ReloadOption func(*reloadConfig)

type reloadConfig struct {
	reason   string
	override map[string]any
	err      error
}

// WithOverride attaches a one-shot in-memory layer to this reload,
// merged above CLI flags. Override values must be JSON-serializable.
// Reload deep-copies the override when it applies this option, producing
// an independent JSON-shaped map before the reload request enters the
// pipeline. Callers may freely mutate or reuse the original after Reload
// returns. The layer is not remembered: a subsequent Reload reverts to the
// natural state.
//
// Use cases: targeted integration tests and ad-hoc operator overrides
// without writing a file. Reload runs the full pipeline; keep it off hot read paths.
func WithOverride(override map[string]any) ReloadOption {
	return func(c *reloadConfig) {
		copied, err := cloneOverride(override)
		if err != nil {
			c.err = fmt.Errorf("%w: WithOverride: %v", fcerr.ErrDecode, err)
			return
		}
		c.override = copied
	}
}

// cloneOverride returns a fully-independent JSON-shaped copy of m.
// JSON round-tripping deliberately defines the accepted override shape:
// map/object, slice/array, string, bool, number and nil values. Pointer
// and struct values that json.Marshal accepts are materialised into that
// tree, so later caller-side mutations cannot alias the reload pipeline.
func cloneOverride(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	buf, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.UseNumber()
	out := map[string]any{}
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// WithReason overrides the default "manual" reason tag stamped on
// the audit / metric / log lines this reload emits.
func WithReason(reason string) ReloadOption {
	return func(c *reloadConfig) {
		if reason != "" {
			c.reason = reason
		}
	}
}

func overrideReloadReason(reason string) string {
	if reason == "manual" {
		return "override"
	}
	return reason
}

// reload runs one pipeline with reload and stage observation. key identifies
// the watched directory, if any; extra layers join the normal priority order.
func (m *Manager[T]) reload(ctx context.Context, reason, key string, extra ...stagedLayer) error {
	start := time.Now()
	if m.observed() {
		m.emit(ReloadStarted{Reason: reason})
	}
	m.opts.Logger.LogAttrs(context.Background(), slog.LevelDebug, "fastconf reload start", slog.String("reason", reason))

	ctx, root := m.startSpan(ctx, "fastconf.reload")
	root.SetAttribute("reason", reason)
	root.SetAttribute("generation", int64(m.gen.Load()))
	defer root.End()

	var assembly assemblyResult
	_, err := m.timed(ctx, "fastconf.assemble", func(ctx context.Context, sp Span) (err error) {
		assembly, err = m.assemble(ctx, "", extra...)
		if err == nil {
			sp.SetAttribute("layers", int64(len(assembly.staged)))
		}
		return err
	})
	// Watch every scanned directory whether or not this reload publishes:
	// a new source with an unchanged value, or a broken file in a new
	// overlay, must still be observed on its next edit.
	m.refreshWatchPaths(assembly.watchDirs)
	if err != nil {
		root.RecordError(err)
		m.emitReload(reason, time.Since(start), err)
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf reload shadow_failed", slog.String("reason", reason), slog.Any("err", err))
		return err
	}
	_, err = m.timed(ctx, "fastconf.commit", func(ctx context.Context, _ Span) error { return m.commit(ctx, assembly, reason, key) })
	if err != nil {
		root.RecordError(err)
		m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf reload commit_failed", slog.String("reason", reason), slog.Any("err", err))
	}
	m.emitReload(reason, time.Since(start), err)
	return err
}
