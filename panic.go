package fastconf

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/fastabc/fastconf/internal/fcerr"
)

// recoverPanic converts a panic in user code into an ErrPanic
// that names where it happened. Call it deferred with the function's named
// error result. The stack goes to the log, not the error, so Errors entries
// and wrapped messages stay short.
func recoverPanic(ctx context.Context, logger *slog.Logger, where string, err *error) {
	v := recover()
	if v == nil {
		return
	}
	logger.LogAttrs(ctx, slog.LevelError, "fastconf panic recovered",
		slog.String("stage", where), slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
	*err = fmt.Errorf("%w in %s: %v", fcerr.ErrPanic, where, v)
}
