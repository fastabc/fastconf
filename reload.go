package fastconf

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// Reload synchronously reloads sources, preserving the published state on failure.
// WithOverride adds a copied one-shot layer; WithReason sets the audit reason.
// Do not mutate override input concurrently with this call.
func (m *Manager[T]) Reload(ctx context.Context, opts ...ReloadOption) error {
	if m == nil {
		return ErrClosed
	}
	cfg := reloadConfig{reason: "manual"}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&cfg)
	}
	if cfg.err != nil {
		// Option errors fail on the reload goroutine like any other reload,
		// so the Errors() channel keeps a single writer.
		optErr := cfg.err
		return m.submitAndWait(ctx, reloadRequest{
			ctx:     ctx,
			trigger: TriggerManual,
			reason:  overrideReloadReason(cfg.reason),
			run:     func(context.Context) error { return optErr },
		})
	}
	if cfg.override == nil {
		return m.requestReload(ctx, TriggerManual, cfg.reason, "")
	}
	extra := stagedLayer{
		src: SourceRef{
			Path:     "override://once",
			Kind:     LayerOverride,
			Priority: rankOffsetOverride,
		},
		class: classOverride,
		data:  cfg.override,
	}
	reason := overrideReloadReason(cfg.reason)
	return m.submitAndWait(ctx, reloadRequest{
		ctx:     ctx,
		trigger: TriggerManual,
		reason:  reason,
		run: func(pipeCtx context.Context) error {
			return m.reload(pipeCtx, TriggerManual, reason, "", extra)
		},
	})
}

// ReloadOption tunes a single Reload invocation. nil values are ignored.
type ReloadOption func(*reloadConfig)

type reloadConfig struct {
	reason   string
	override map[string]any
	err      error
}

// WithOverride adds a one-shot layer above all sources. Values must be JSON-serializable.
// Reload copies the map while applying options; do not mutate it concurrently with
// Reload. After Reload returns, callers may mutate or reuse it. Later reloads use
// natural sources again. See docs/cookbook/runtime/reload-policy.md.
func WithOverride(override map[string]any) ReloadOption {
	return func(c *reloadConfig) {
		copied, err := encodeJSONTree(override)
		if err != nil {
			c.err = fmt.Errorf("%w: WithOverride: %v", fcerr.ErrDecode, err)
			return
		}
		c.override = copied
	}
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

// reload runs one pipeline with reload and stage observation. trigger and
// reason are stamped on a published cause; key identifies the watched
// directory, if any; extra layers join the normal priority order.
func (m *Manager[T]) reload(ctx context.Context, trigger ReloadTrigger, reason, key string, extra ...stagedLayer) error {
	start := time.Now()
	if m.hasObservers() {
		m.emit(ReloadStarted{Reason: reason, Trigger: trigger})
	}
	m.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "fastconf reload start", slog.String("reason", reason))

	ctx, root := m.startSpan(ctx, "fastconf.reload")
	root.SetAttribute("reason", reason)
	root.SetAttribute("trigger", string(trigger))
	root.SetAttribute("generation", int64(m.gen.Load()))
	defer root.End()

	var assembly assemblyResult
	var onDir func(string)
	if m.fileWatcher != nil {
		onDir = func(dir string) { m.watchSourceDir(ctx, dir) }
	}
	_, err := m.runTimedStage(ctx, trigger, "fastconf.assemble", func(ctx context.Context, sp Span) (err error) {
		assembly, err = m.assembleSources(ctx, "", onDir, m.publishedFingerprint, extra...)
		if err == nil {
			sp.SetAttribute("layers", int64(len(assembly.staged)))
		}
		return err
	})
	if err != nil {
		root.RecordError(err)
		m.emitReload(reason, trigger, time.Since(start), err)
		m.opts.Logger.LogAttrs(ctx, slog.LevelWarn, "fastconf reload assemble_failed", slog.String("reason", reason), slog.Any("err", err))
		return err
	}
	_, err = m.runTimedStage(ctx, trigger, "fastconf.commit", func(ctx context.Context, _ Span) error {
		return m.commit(ctx, assembly, trigger, reason, key)
	})
	if err != nil {
		root.RecordError(err)
		m.opts.Logger.LogAttrs(ctx, slog.LevelWarn, "fastconf reload commit_failed", slog.String("reason", reason), slog.Any("err", err))
	}
	m.emitReload(reason, trigger, time.Since(start), err)
	return err
}
