package contracts

import "context"

// Notifier refreshes configuration before acknowledging an external event.
// Run should stop when ctx is canceled; Manager restarts it on early return.
type Notifier interface {
	Run(ctx context.Context, refresh func(context.Context) error) error
}
