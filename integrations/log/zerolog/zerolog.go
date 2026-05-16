// Package zerolog adapts a zerolog.Logger into an slog.Handler so that
// FastConf's WithLogger entry point can deliver structured
// FastConf events to a zerolog backend without bringing the zerolog
// dependency into the FastConf root module.
//
// Usage:
//
//	zl := zerolog.New(os.Stderr).With().Timestamp().Logger()
//	cfg, _ := fastconf.New[AppConfig](ctx,
//	    fastconf.WithLogger(slog.New(zerologadapter.NewHandler(zl, zerologadapter.Options{}))),
//	)
//
// All FastConf log lines flow through zerolog with their attrs preserved
// as structured fields. Groups (slog.Group / Logger.WithGroup) are encoded
// as dotted key prefixes — e.g. a group "stage" followed by attr {"name":
// "decode"} appears as field "stage.name=decode".
package zerolog

import (
	"context"
	"log/slog"
	"runtime"
	"strconv"

	zlog "github.com/rs/zerolog"
)

// Options configures NewHandler.
type Options struct {
	// Level is an optional slog-side gate. Nil (the default) means "no
	// slog-side filtering — defer fully to the underlying zerolog.Logger
	// level". Set to a slog.LevelVar or a fixed slog.Level value to add a
	// secondary, hot-reloadable gate on top.
	Level slog.Leveler
	// AddSource, when true, includes the call site (file:line) as a "source"
	// field. Default false.
	AddSource bool
	// GroupSeparator joins nested slog.Group prefixes (e.g. "stage.name").
	// Default ".".
	GroupSeparator string
}

// NewHandler wraps a zerolog.Logger into an slog.Handler. The logger is
// captured by value; subsequent zerolog.Logger.Level / sample / context
// changes on the original variable do not affect this handler.
func NewHandler(l zlog.Logger, opts Options) slog.Handler {
	if opts.GroupSeparator == "" {
		opts.GroupSeparator = "."
	}
	return &handler{l: l, opts: opts}
}

type handler struct {
	l       zlog.Logger
	opts    Options
	attrs   []slog.Attr
	groups  []string
	groupPrefix string
}

func (h *handler) Enabled(_ context.Context, lvl slog.Level) bool {
	if h.opts.Level != nil && lvl < h.opts.Level.Level() {
		return false
	}
	return zlevel(lvl) >= h.l.GetLevel()
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	ev := h.l.WithLevel(zlevel(r.Level))
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
	n.groups = append(append([]string(nil), h.groups...), name)
	if h.groupPrefix == "" {
		n.groupPrefix = name + h.opts.GroupSeparator
	} else {
		n.groupPrefix = h.groupPrefix + name + h.opts.GroupSeparator
	}
	return &n
}

// zlevel maps slog.Level into the nearest zerolog level.
func zlevel(l slog.Level) zlog.Level {
	switch {
	case l >= slog.LevelError:
		return zlog.ErrorLevel
	case l >= slog.LevelWarn:
		return zlog.WarnLevel
	case l >= slog.LevelInfo:
		return zlog.InfoLevel
	case l >= slog.LevelDebug:
		return zlog.DebugLevel
	default:
		return zlog.TraceLevel
	}
}

// appendAttr emits a single slog.Attr onto the in-flight zerolog event,
// recursing into groups and joining nested keys with sep.
func appendAttr(ev *zlog.Event, prefix string, a slog.Attr, sep string) *zlog.Event {
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
		return ev.Interface(key, v.Any())
	default:
		return ev.Interface(key, v.Any())
	}
}

// sourceFor renders a "file:line" string for the given program counter,
// paid only when AddSource is enabled.
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
