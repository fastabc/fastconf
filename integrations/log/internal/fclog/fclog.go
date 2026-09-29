// Package fclog implements the backend-independent half of the FastConf
// logging adapters: slog level mapping, group scoping, attr flattening and
// call-site rendering. Each adapter module (phuslu, zerolog) only supplies a
// Backend that creates per-record Events for its concrete logger, so neither
// logging library is imported here and neither adapter depends on the other.
package fclog

import (
	"context"
	"log/slog"
	"runtime"
	"strconv"
	"time"
)

// Level is the backend-neutral severity every adapter maps onto its own
// level type.
type Level int8

const (
	TraceLevel Level = iota
	DebugLevel
	InfoLevel
	WarnLevel
	ErrorLevel
)

// LevelOf maps slog.Level into the nearest backend-neutral level.
func LevelOf(l slog.Level) Level {
	switch {
	case l >= slog.LevelError:
		return ErrorLevel
	case l >= slog.LevelWarn:
		return WarnLevel
	case l >= slog.LevelInfo:
		return InfoLevel
	case l >= slog.LevelDebug:
		return DebugLevel
	default:
		return TraceLevel
	}
}

// Event receives the fields of one in-flight record; Msg writes it.
// Implementations wrap a single backend pointer so storing one in the
// interface does not allocate.
type Event interface {
	Str(key, val string)
	Int64(key string, val int64)
	Uint64(key string, val uint64)
	Float64(key string, val float64)
	Bool(key string, val bool)
	Dur(key string, val time.Duration)
	Time(key string, val time.Time)
	Err(key string, err error)
	Any(key string, val any)
	Msg(msg string)
}

// Backend adapts a concrete logger.
type Backend interface {
	// Enabled reports whether the backend's own level admits lvl.
	Enabled(lvl Level) bool
	// Event starts a record at lvl, or returns nil when it is dropped.
	Event(lvl Level) Event
}

// Options configures NewHandler. The adapter packages declare identical
// public structs and convert them to this type.
type Options struct {
	Level          slog.Leveler
	AddSource      bool
	GroupSeparator string
}

// NewHandler wraps b into an slog.Handler. A nil Backend yields a handler
// that discards everything.
func NewHandler(b Backend, opts Options) slog.Handler {
	if b == nil {
		return noopHandler{}
	}
	if opts.GroupSeparator == "" {
		opts.GroupSeparator = "."
	}
	return &handler{b: b, opts: opts}
}

type handler struct {
	b    Backend
	opts Options
	// attrs keeps each WithAttrs batch with the group prefix active when it
	// was added, so a later WithGroup does not re-home earlier attrs.
	attrs       []boundAttr
	groupPrefix string
}

type boundAttr struct {
	prefix string
	attr   slog.Attr
}

func (h *handler) Enabled(_ context.Context, lvl slog.Level) bool {
	if h.opts.Level != nil && lvl < h.opts.Level.Level() {
		return false
	}
	return h.b.Enabled(LevelOf(lvl))
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	ev := h.b.Event(LevelOf(r.Level))
	if ev == nil {
		return nil
	}
	if h.opts.AddSource && r.PC != 0 {
		ev.Str("source", sourceFor(r.PC))
	}
	if !r.Time.IsZero() {
		ev.Time("time", r.Time)
	}
	for _, b := range h.attrs {
		appendAttr(ev, b.prefix, b.attr, h.opts.GroupSeparator)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(ev, h.groupPrefix, a, h.opts.GroupSeparator)
		return true
	})
	ev.Msg(r.Message)
	return nil
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	n := *h
	n.attrs = make([]boundAttr, len(h.attrs), len(h.attrs)+len(as))
	copy(n.attrs, h.attrs)
	for _, a := range as {
		n.attrs = append(n.attrs, boundAttr{prefix: h.groupPrefix, attr: a})
	}
	return &n
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.groupPrefix = h.groupPrefix + name + h.opts.GroupSeparator
	return &n
}

// appendAttr emits a single slog.Attr onto ev, recursing into groups and
// joining nested keys with sep.
func appendAttr(ev Event, prefix string, a slog.Attr, sep string) {
	if a.Equal(slog.Attr{}) {
		return
	}
	v := a.Value.Resolve()
	key := prefix + a.Key
	switch v.Kind() {
	case slog.KindString:
		ev.Str(key, v.String())
	case slog.KindInt64:
		ev.Int64(key, v.Int64())
	case slog.KindUint64:
		ev.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		ev.Float64(key, v.Float64())
	case slog.KindBool:
		ev.Bool(key, v.Bool())
	case slog.KindDuration:
		ev.Dur(key, v.Duration())
	case slog.KindTime:
		ev.Time(key, v.Time())
	case slog.KindGroup:
		inner := key + sep
		if a.Key == "" {
			inner = prefix
		}
		for _, ga := range v.Group() {
			appendAttr(ev, inner, ga, sep)
		}
	default:
		if err, ok := v.Any().(error); ok {
			ev.Err(key, err)
			return
		}
		ev.Any(key, v.Any())
	}
}

// sourceFor renders a "file:line" string for the given program counter,
// paid only when AddSource is enabled.
func sourceFor(pc uintptr) string {
	frames := runtime.CallersFrames([]uintptr{pc})
	fr, _ := frames.Next()
	if fr.File == "" {
		return ""
	}
	return fr.File + ":" + strconv.Itoa(fr.Line)
}

// noopHandler discards every record, so adapters can accept a nil logger.
type noopHandler struct{}

func (noopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (noopHandler) Handle(context.Context, slog.Record) error { return nil }
func (noopHandler) WithAttrs([]slog.Attr) slog.Handler        { return noopHandler{} }
func (noopHandler) WithGroup(string) slog.Handler             { return noopHandler{} }
