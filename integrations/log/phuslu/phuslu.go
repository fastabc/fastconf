// Package phuslu adapts a *phuslu/log.Logger into an slog.Handler so that
// FastConf's WithLogger entry point can deliver structured FastConf
// events to phuslu/log without bringing the phuslu/log dependency into
// the FastConf root module.
//
// Usage:
//
//	pl := &log.Logger{
//	    Level:      log.InfoLevel,
//	    TimeFormat: "2006-01-02T15:04:05.999Z07:00",
//	    Writer:     &log.IOWriter{Writer: os.Stderr},
//	}
//	cfg, _ := fastconf.New[AppConfig](ctx,
//	    fastconf.WithLogger(slog.New(phusluadapter.NewHandler(pl, phusluadapter.Options{}))),
//	)
//
// All FastConf log lines flow through phuslu/log with their attrs preserved
// as structured fields. Groups (slog.Group / Logger.WithGroup) are encoded
// as dotted key prefixes — e.g. a group "stage" followed by attr {"name":
// "decode"} appears as field "stage.name=decode".
package phuslu

import (
	"context"
	"log/slog"
	"runtime"
	"strconv"

	plog "github.com/phuslu/log"
)

// Options configures NewHandler.
type Options struct {
	// Level is an optional slog-side gate. Nil (the default) means "no
	// slog-side filtering — defer fully to the underlying *phuslu/log.Logger
	// Level field". Set to a slog.LevelVar or a fixed slog.Level value to
	// add a secondary, hot-reloadable gate on top.
	Level slog.Leveler
	// AddSource, when true, includes the call site (file:line) as a "source"
	// field. Default false.
	AddSource bool
	// GroupSeparator joins nested slog.Group prefixes (e.g. "stage.name").
	// Default ".".
	GroupSeparator string
}

// NewHandler wraps a *phuslu/log.Logger into an slog.Handler. The Logger
// pointer is captured directly; mutations to its Level / Writer / etc. take
// effect immediately for subsequent log records.
//
// Passing nil installs a no-op handler so callers do not have to special
// case "logging disabled".
func NewHandler(l *plog.Logger, opts Options) slog.Handler {
	if l == nil {
		return noopHandler{}
	}
	if opts.GroupSeparator == "" {
		opts.GroupSeparator = "."
	}
	return &handler{l: l, opts: opts}
}

type handler struct {
	l           *plog.Logger
	opts        Options
	attrs       []slog.Attr
	groupPrefix string
}

func (h *handler) Enabled(_ context.Context, lvl slog.Level) bool {
	if h.opts.Level != nil && lvl < h.opts.Level.Level() {
		return false
	}
	return uint32(plevel(lvl)) >= uint32(h.l.Level)
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	ev := h.l.WithLevel(plevel(r.Level))
	if ev == nil {
		return nil
	}
	if h.opts.AddSource && r.PC != 0 {
		ev = ev.Str("source", sourceFor(r.PC))
	}
	if !r.Time.IsZero() {
		ev = ev.Time("time", r.Time)
	}
	for _, a := range h.attrs {
		ev = appendAttr(ev, h.groupPrefix, a, h.opts.GroupSeparator)
	}
	r.Attrs(func(a slog.Attr) bool {
		ev = appendAttr(ev, h.groupPrefix, a, h.opts.GroupSeparator)
		return true
	})
	ev.Msg(r.Message)
	return nil
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr(nil), h.attrs...), as...)
	return &n
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	if h.groupPrefix == "" {
		n.groupPrefix = name + h.opts.GroupSeparator
	} else {
		n.groupPrefix = h.groupPrefix + name + h.opts.GroupSeparator
	}
	return &n
}

// plevel maps slog.Level into the nearest phuslu/log level.
func plevel(l slog.Level) plog.Level {
	switch {
	case l >= slog.LevelError:
		return plog.ErrorLevel
	case l >= slog.LevelWarn:
		return plog.WarnLevel
	case l >= slog.LevelInfo:
		return plog.InfoLevel
	case l >= slog.LevelDebug:
		return plog.DebugLevel
	default:
		return plog.TraceLevel
	}
}

// appendAttr emits a single slog.Attr onto the in-flight phuslu/log Entry,
// recursing into groups and joining nested keys with sep.
func appendAttr(ev *plog.Entry, prefix string, a slog.Attr, sep string) *plog.Entry {
	if a.Equal(slog.Attr{}) {
		return ev
	}
	v := a.Value.Resolve()
	key := prefix + a.Key
	switch v.Kind() {
	case slog.KindString:
		return ev.Str(key, v.String())
	case slog.KindInt64:
		return ev.Int64(key, v.Int64())
	case slog.KindUint64:
		return ev.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		return ev.Float64(key, v.Float64())
	case slog.KindBool:
		return ev.Bool(key, v.Bool())
	case slog.KindDuration:
		return ev.Dur(key, v.Duration())
	case slog.KindTime:
		return ev.Time(key, v.Time())
	case slog.KindGroup:
		inner := key + sep
		for _, ga := range v.Group() {
			ev = appendAttr(ev, inner, ga, sep)
		}
		return ev
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return ev.AnErr(key, err)
		}
		return ev.Any(key, v.Any())
	default:
		return ev.Any(key, v.Any())
	}
}

// sourceFor renders a "file:line" string for the given program counter.
func sourceFor(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frames := runtime.CallersFrames([]uintptr{pc})
	fr, _ := frames.Next()
	if fr.File == "" {
		return ""
	}
	return fr.File + ":" + strconv.Itoa(fr.Line)
}

// noopHandler is returned when NewHandler is called with a nil Logger so
// callers can opt out without writing their own no-op.
type noopHandler struct{}

func (noopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (noopHandler) Handle(context.Context, slog.Record) error { return nil }
func (noopHandler) WithAttrs([]slog.Attr) slog.Handler        { return noopHandler{} }
func (noopHandler) WithGroup(string) slog.Handler             { return noopHandler{} }
