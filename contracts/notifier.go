package contracts

import "context"

// Notifier refreshes configuration before acknowledging an external event.
// Run should stop when ctx is canceled; Manager restarts it on early return.
// Only a nil refresh result allows acknowledgement. On any error, retain or
// negatively acknowledge the event according to the transport's retry policy.
type Notifier interface {
	Run(ctx context.Context, refresh func(context.Context) error) error
}
