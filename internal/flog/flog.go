// Package flog wraps *slog.Logger with a zerolog-style fluent API while
// preserving slog's handler ecosystem.
//
// The underlying transport remains a *slog.Logger, so any slog.Handler —
// the stdlib JSON/Text handlers, the phuslu and zerolog adapters under
// integrations/log/, or any third-party Handler — works unchanged. Callers
// pick the backend by passing the appropriate *slog.Logger via
// fastconf.WithLogger.
//
// Usage:
//
//	log := flog.New(slog.Default())
//	log.Info().
//	    Str("reason", reason).
//	    Uint64("generation", gen).
//	    Int("layers", n).
//	    Err(err).
//	    Msg("reload swap")
//
// At a disabled level Info()/Debug()/etc. return nil and every fluent
// method short-circuits on nil, so the chain costs only the level check.
// Events are pooled, so each emit is amortized zero-allocation aside from
// the slog.Record path inside the chosen Handler.
package flog

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

// Logger wraps a *slog.Logger with fluent builder methods. A zero-value
// Logger is invalid; use New().
type Logger struct{ s *slog.Logger }

// New wraps an existing *slog.Logger. If s is nil, slog.Default() is used.
func New(s *slog.Logger) *Logger {
	if s == nil {
		s = slog.Default()
	}
	return &Logger{s: s}
}

// Slog returns the underlying *slog.Logger for interop with slog-typed APIs.
func (l *Logger) Slog() *slog.Logger { return l.s }

// Event is a level-gated, attribute-accumulating builder. A nil *Event is
// a no-op — every fluent method tolerates nil so disabled-level chains
// short-circuit after the initial level check.
type Event struct {
	l     *slog.Logger
	ctx   context.Context
	lvl   slog.Level
	attrs []slog.Attr
}

var eventPool = sync.Pool{New: func() any { return &Event{attrs: make([]slog.Attr, 0, 8)} }}

func (l *Logger) at(ctx context.Context, lvl slog.Level) *Event {
	if !l.s.Enabled(ctx, lvl) {
		return nil
	}
	e := eventPool.Get().(*Event)
	e.l = l.s
	e.ctx = ctx
	e.lvl = lvl
	e.attrs = e.attrs[:0]
	return e
}

// Debug starts a Debug-level event. Returns nil if the level is disabled.
func (l *Logger) Debug() *Event { return l.at(context.Background(), slog.LevelDebug) }

// Info starts an Info-level event.
func (l *Logger) Info() *Event { return l.at(context.Background(), slog.LevelInfo) }

// Warn starts a Warn-level event.
func (l *Logger) Warn() *Event { return l.at(context.Background(), slog.LevelWarn) }

// Error starts an Error-level event.
func (l *Logger) Error() *Event { return l.at(context.Background(), slog.LevelError) }

// DebugCtx starts a Debug-level event carrying ctx.
func (l *Logger) DebugCtx(ctx context.Context) *Event { return l.at(ctx, slog.LevelDebug) }

// InfoCtx starts an Info-level event carrying ctx.
func (l *Logger) InfoCtx(ctx context.Context) *Event { return l.at(ctx, slog.LevelInfo) }

// WarnCtx starts a Warn-level event carrying ctx.
func (l *Logger) WarnCtx(ctx context.Context) *Event { return l.at(ctx, slog.LevelWarn) }

// ErrorCtx starts an Error-level event carrying ctx.
func (l *Logger) ErrorCtx(ctx context.Context) *Event { return l.at(ctx, slog.LevelError) }

// At starts an event at an arbitrary slog.Level (use for custom levels).
func (l *Logger) At(lvl slog.Level) *Event { return l.at(context.Background(), lvl) }

// AtCtx starts an event at an arbitrary slog.Level carrying ctx.
func (l *Logger) AtCtx(ctx context.Context, lvl slog.Level) *Event { return l.at(ctx, lvl) }

// ---- field methods ----

func (e *Event) Str(k, v string) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.String(k, v))
	return e
}

func (e *Event) Strs(k string, v []string) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Any(k, v))
	return e
}

func (e *Event) Int(k string, v int) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Int(k, v))
	return e
}

func (e *Event) Int64(k string, v int64) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Int64(k, v))
	return e
}

func (e *Event) Uint64(k string, v uint64) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Uint64(k, v))
	return e
}

func (e *Event) Float64(k string, v float64) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Float64(k, v))
	return e
}

func (e *Event) Bool(k string, v bool) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Bool(k, v))
	return e
}

// Dur attaches a time.Duration. Stdlib slog renders it as a nanosecond
// int64 by default; the phuslu and zerolog adapters render it in their
// native duration form.
func (e *Event) Dur(k string, v time.Duration) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Duration(k, v))
	return e
}

func (e *Event) Time(k string, v time.Time) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Time(k, v))
	return e
}

// Err attaches an error under the conventional key "err". Nil errors are
// silently skipped so chains can stay flat (.Err(err) instead of an if).
func (e *Event) Err(err error) *Event {
	if e == nil || err == nil {
		return e
	}
	e.attrs = append(e.attrs, slog.Any("err", err))
	return e
}

// NamedErr attaches an error under a caller-chosen key. Nil errors are
// skipped, matching Err.
func (e *Event) NamedErr(k string, err error) *Event {
	if e == nil || err == nil {
		return e
	}
	e.attrs = append(e.attrs, slog.Any(k, err))
	return e
}

// Any falls back to slog.Any for slices, maps, or arbitrary values.
func (e *Event) Any(k string, v any) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, slog.Any(k, v))
	return e
}

// Attr passes a pre-built slog.Attr (e.g. slog.Group(...)) into the chain.
func (e *Event) Attr(a slog.Attr) *Event {
	if e == nil {
		return nil
	}
	e.attrs = append(e.attrs, a)
	return e
}

// Msg emits the event with msg and returns the *Event to the pool. After
// Msg() the receiver must not be reused.
func (e *Event) Msg(msg string) {
	if e == nil {
		return
	}
	// Capture the call site (the line that invoked Msg). Handlers that
	// honour slog.Record.PC (stdlib AddSource, phuslu/zerolog adapters)
	// will surface it as the source line.
	var pcs [1]uintptr
	var pc uintptr
	if n := runtime.Callers(2, pcs[:]); n >= 1 {
		pc = pcs[0]
	}
	r := slog.NewRecord(time.Now(), e.lvl, msg, pc)
	r.AddAttrs(e.attrs...)
	_ = e.l.Handler().Handle(e.ctx, r)
	e.attrs = e.attrs[:0]
	eventPool.Put(e)
}

// Send is shorthand for Msg("").
func (e *Event) Send() { e.Msg("") }

// ---- derived logger (zerolog "With" chain) ----

// Context is the builder returned by Logger.With(); call Logger() to
// materialize a derived *Logger that attaches the accumulated attrs to
// every record it emits.
type Context struct{ s *slog.Logger }

// With opens a sub-logger builder. Use at startup or per request — each
// step allocates a new *slog.Logger, so With() is not a hot-path API.
func (l *Logger) With() *Context { return &Context{s: l.s} }

func (c *Context) Str(k, v string) *Context           { c.s = c.s.With(slog.String(k, v)); return c }
func (c *Context) Int(k string, v int) *Context       { c.s = c.s.With(slog.Int(k, v)); return c }
func (c *Context) Int64(k string, v int64) *Context   { c.s = c.s.With(slog.Int64(k, v)); return c }
func (c *Context) Uint64(k string, v uint64) *Context { c.s = c.s.With(slog.Uint64(k, v)); return c }
func (c *Context) Bool(k string, v bool) *Context     { c.s = c.s.With(slog.Bool(k, v)); return c }
func (c *Context) Dur(k string, v time.Duration) *Context {
	c.s = c.s.With(slog.Duration(k, v))
	return c
}
func (c *Context) Time(k string, v time.Time) *Context { c.s = c.s.With(slog.Time(k, v)); return c }
func (c *Context) Any(k string, v any) *Context        { c.s = c.s.With(slog.Any(k, v)); return c }

// Group nests subsequent records under name (slog.Logger.WithGroup semantics).
func (c *Context) Group(name string) *Context { c.s = c.s.WithGroup(name); return c }

// Logger seals the chain and returns the derived *Logger.
func (c *Context) Logger() *Logger { return &Logger{s: c.s} }
