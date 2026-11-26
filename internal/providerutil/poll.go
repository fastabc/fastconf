package providerutil

import "context"

// PollReporter passes polling failure transitions to an optional handler: the
// error when polls start failing and nil on the first success after that.
// Errors after ctx is canceled are not polling failures. Use one value per
// polling goroutine; it is not safe for concurrent use.
type PollReporter struct {
	Handler func(error)
	failing bool
}

// Report records the result of one poll.
func (r *PollReporter) Report(ctx context.Context, err error) {
	if r.Handler == nil || ctx.Err() != nil || (err != nil) == r.failing {
		return
	}
	r.failing = err != nil
	r.Handler(err)
}
