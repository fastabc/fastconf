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
//
// This package is its own module; it shares the slog handling with the
// phuslu adapter through the dependency-free integrations/log module, so
// importing it never pulls in phuslu/log.
package zerolog

import (
	"log/slog"
	"time"

	zlog "github.com/rs/zerolog"

	"github.com/fastabc/fastconf/integrations/log/internal/fclog"
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
	return fclog.NewHandler(&backend{l}, fclog.Options(opts))
}

type backend struct{ l zlog.Logger }

func (b *backend) Enabled(lvl fclog.Level) bool {
	return zlevel(lvl) >= b.l.GetLevel()
}

func (b *backend) Event(lvl fclog.Level) fclog.Event {
	if e := b.l.WithLevel(zlevel(lvl)); e != nil {
		return event{e}
	}
	return nil
}

// zlevel maps a fclog level into the matching zerolog level.
func zlevel(l fclog.Level) zlog.Level {
	switch l {
	case fclog.ErrorLevel:
		return zlog.ErrorLevel
	case fclog.WarnLevel:
		return zlog.WarnLevel
	case fclog.InfoLevel:
		return zlog.InfoLevel
	case fclog.DebugLevel:
		return zlog.DebugLevel
	default:
		return zlog.TraceLevel
	}
}

// event forwards fields to the in-flight zerolog Event.
type event struct{ e *zlog.Event }

func (v event) Str(k, s string)               { v.e.Str(k, s) }
func (v event) Int64(k string, n int64)       { v.e.Int64(k, n) }
func (v event) Uint64(k string, n uint64)     { v.e.Uint64(k, n) }
func (v event) Float64(k string, f float64)   { v.e.Float64(k, f) }
func (v event) Bool(k string, b bool)         { v.e.Bool(k, b) }
func (v event) Dur(k string, d time.Duration) { v.e.Dur(k, d) }
func (v event) Time(k string, t time.Time)    { v.e.Time(k, t) }
func (v event) Err(k string, err error)       { v.e.AnErr(k, err) }
func (v event) Any(k string, a any)           { v.e.Interface(k, a) }
func (v event) Msg(msg string)                { v.e.Msg(msg) }
