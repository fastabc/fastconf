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
//
// This package is its own module; it shares the slog handling with the
// zerolog adapter through the dependency-free integrations/log module, so
// importing it never pulls in zerolog.
package phuslu

import (
	"log/slog"
	"time"

	plog "github.com/phuslu/log"

	"github.com/fastabc/fastconf/integrations/log/internal/fclog"
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
	var b fclog.Backend
	if l != nil {
		b = backend{l}
	}
	return fclog.NewHandler(b, fclog.Options(opts))
}

type backend struct{ l *plog.Logger }

func (b backend) Enabled(lvl fclog.Level) bool {
	return plevel(lvl) >= b.l.Level
}

func (b backend) Event(lvl fclog.Level) fclog.Event {
	if e := b.l.WithLevel(plevel(lvl)); e != nil {
		return event{e}
	}
	return nil
}

// plevel maps a fclog level into the matching phuslu/log level.
func plevel(l fclog.Level) plog.Level {
	switch l {
	case fclog.ErrorLevel:
		return plog.ErrorLevel
	case fclog.WarnLevel:
		return plog.WarnLevel
	case fclog.InfoLevel:
		return plog.InfoLevel
	case fclog.DebugLevel:
		return plog.DebugLevel
	default:
		return plog.TraceLevel
	}
}

// event forwards fields to the in-flight phuslu/log Entry.
type event struct{ e *plog.Entry }

func (v event) Str(k, s string)               { v.e.Str(k, s) }
func (v event) Int64(k string, n int64)       { v.e.Int64(k, n) }
func (v event) Uint64(k string, n uint64)     { v.e.Uint64(k, n) }
func (v event) Float64(k string, f float64)   { v.e.Float64(k, f) }
func (v event) Bool(k string, b bool)         { v.e.Bool(k, b) }
func (v event) Dur(k string, d time.Duration) { v.e.Dur(k, d) }
func (v event) Time(k string, t time.Time)    { v.e.Time(k, t) }
func (v event) Err(k string, err error)       { v.e.AnErr(k, err) }
func (v event) Any(k string, a any)           { v.e.Any(k, a) }
func (v event) Msg(msg string)                { v.e.Msg(msg) }
