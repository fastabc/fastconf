// Package azservicebus refreshes configuration from Event Grid notifications
// delivered to a dedicated Service Bus queue or topic subscription in PeekLock
// mode. The application owns the receiver and its lifecycle.
package azservicebus

import (
	"context"
	"errors"
	"log/slog"
	"time"

	sdk "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// Source selects the service's notification schema.
type Source string

const (
	AppConfiguration Source = "azappconfig"
	Blob             Source = "azblob"
)

// Config selects an exact Event Grid resource ID and one source selection.
// Prefix/Label select App Configuration settings. Container/Key select a blob.
type Config struct {
	Source     Source
	ResourceID string

	Prefix string // AppConfiguration key prefix.
	Label  string // AppConfiguration exact label; empty selects unlabelled settings.

	Container string // Required for Blob.
	Key       string // Required for Blob; exact object key.

	RefreshTimeout time.Duration // Default 30s, further bounded by the message lock.
	OnError        func(error)   // Nil uses slog; must not block or panic.
}

// API is implemented by a *azservicebus.Receiver opened in PeekLock mode.
type API interface {
	ReceiveMessages(context.Context, int, *sdk.ReceiveMessagesOptions) ([]*sdk.ReceivedMessage, error)
	CompleteMessage(context.Context, *sdk.ReceivedMessage, *sdk.CompleteMessageOptions) error
	AbandonMessage(context.Context, *sdk.ReceivedMessage, *sdk.AbandonMessageOptions) error
}

// Consumer refreshes configuration before requesting message acknowledgement.
type Consumer struct {
	cfg      Config
	receiver API
}

// New validates source selection and borrows the configured receiver.
// The application owns its lifecycle; cloud resources must already exist.
func New(cfg Config, receiver API) (*Consumer, error) {
	if receiver == nil || cfg.ResourceID == "" {
		return nil, errors.New("fastconf/azservicebus: receiver and resource ID are required")
	}
	switch cfg.Source {
	case AppConfiguration:
		// Empty prefix and label select all unlabelled settings.
	case Blob:
		if cfg.Container == "" || cfg.Key == "" {
			return nil, errors.New("fastconf/azservicebus: container and key are required")
		}
	default:
		return nil, errors.New("fastconf/azservicebus: unsupported source")
	}

	if cfg.RefreshTimeout == 0 {
		cfg.RefreshTimeout = 30 * time.Second
	}
	if cfg.RefreshTimeout < 0 {
		return nil, errors.New("fastconf/azservicebus: refresh timeout must be positive")
	}
	return &Consumer{cfg: cfg, receiver: receiver}, nil
}

// Run refreshes before settlement. It receives one message at a time and caps
// the refresh deadline one second before lock expiry instead of renewing locks.
// Configure lock duration above the refresh timeout, and a dead-letter policy.
// Receive failures return for supervision; per-message failures are reported.
// Run must not be called concurrently on the same consumer.
// https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus#Receiver.CompleteMessage
func (c *Consumer) Run(ctx context.Context, refresh func(context.Context) error) error {
	if refresh == nil {
		return errors.New("fastconf/azservicebus: refresh callback is required")
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		messages, err := c.receiver.ReceiveMessages(ctx, 1, nil)
		if err != nil {
			return &operationError{"receive", err}
		}

		for _, msg := range messages {
			matched, err := match(c.cfg, msg.Body)
			if msg.LockedUntil == nil {
				return errors.New("fastconf/azservicebus: receiver must use PeekLock mode")
			}
			deadline := minTime(time.Now().Add(c.cfg.RefreshTimeout), msg.LockedUntil.Add(-time.Second))
			if err == nil && matched {
				work, cancel := context.WithDeadline(ctx, deadline)
				err = work.Err()
				if err == nil {
					err = refresh(work)
				}
				if err == nil {
					err = work.Err()
				}
				cancel()
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}

			settle, cancel := context.WithDeadline(ctx, minTime(time.Now().Add(c.cfg.RefreshTimeout), *msg.LockedUntil))
			if err != nil {
				c.report("process", err)
				err = c.receiver.AbandonMessage(settle, msg, nil)
			} else {
				err = c.receiver.CompleteMessage(settle, msg, nil)
			}
			cancel()
			if err != nil {
				c.report("settle", err)
			}
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *Consumer) report(operation string, err error) {
	safe := &operationError{operation, err}
	if c.cfg.OnError != nil {
		c.cfg.OnError(safe)
	} else {
		slog.Warn("fastconf notification error", "error", safe)
	}
}

type operationError struct {
	operation string
	err       error
}

func (e *operationError) Error() string { return "fastconf/azservicebus: " + e.operation + " failed" }
func (e *operationError) Unwrap() error { return e.err }

var _ API = (*sdk.Receiver)(nil)
