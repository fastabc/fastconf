package fclog

import (
	"log/slog"
	"time"
)

// Both assignments freeze method sets in either direction.
type backendShape interface {
	Enabled(Level) bool
	Event(Level) Event
}
type eventShape interface {
	Str(string, string)
	Int64(string, int64)
	Uint64(string, uint64)
	Float64(string, float64)
	Bool(string, bool)
	Dur(string, time.Duration)
	Time(string, time.Time)
	Err(string, error)
	Any(string, any)
	Msg(string)
}

var (
	_ backendShape                        = Backend(nil)
	_ Backend                             = backendShape(nil)
	_ eventShape                          = Event(nil)
	_ Event                               = eventShape(nil)
	_ func(slog.Level) Level              = LevelOf
	_ func(Backend, Options) slog.Handler = NewHandler
	_                                     = Options(struct {
		Level          slog.Leveler
		AddSource      bool
		GroupSeparator string
	}{})
)
