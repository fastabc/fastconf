// Package phuslu adapts its logger to slog.Handler for fastconf.WithLogger.
// Attributes remain structured; groups use dotted prefixes by default. This optional
// module shares handling through fclog without importing the other logging backend.
// See docs/cookbook/observability/log.md for setup.
package phuslu

import (
	"log/slog"
	"time"

	plog "github.com/phuslu/log"

	"github.com/fastabc/fastconf/integrations/log/internal/fclog"
)

// Options configures NewHandler.
type Options struct {
	// Level adds an optional slog gate; nil delegates filtering to the backend.
	// Use slog.LevelVar for a reloadable secondary gate.
	Level slog.Leveler
	// AddSource, when true, includes the call site (file:line) as a "source"
	// field. Default false.
	AddSource bool
	// GroupSeparator joins nested slog.Group prefixes (e.g. "stage.name").
	// Default ".".
	GroupSeparator string
}

// NewHandler captures the logger pointer, so later logger mutations affect records.
// A nil logger installs a no-op handler.
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
