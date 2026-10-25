// Package pubsub refreshes configuration from GCS or Secret Manager events on
// a dedicated Pub/Sub subscription. Each independent replica needs a separate
// subscription or an application broadcast mechanism. The client is borrowed.
package pubsub

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	sdk "cloud.google.com/go/pubsub/v2"
)

// Source selects the service's notification schema.
type Source string

const (
	GCS           Source = "gcs"
	SecretManager Source = "secretmanager"
)

// Config selects an exact bucket/object or full secret resource (without
// /versions). Secret Manager events carry metadata, never the secret payload.
type Config struct {
	Source Source

	Bucket string // Required for GCS.
	Key    string // Required for GCS; exact object key.
	Secret string // Required for SecretManager; full global or regional secret resource.

	RefreshTimeout time.Duration // Default 30s; must fit within SDK lease extension.
	// OnError reports parsing, refresh and callback cancellation errors.
	// Nil uses slog. Calls may overlap; the handler must not block or panic.
	// SDK Ack/Nack calls are asynchronous and do not report errors here.
	OnError func(error)
}

// API is implemented by *pubsub.Subscriber from cloud.google.com/go/pubsub/v2.
// Configure ReceiveSettings before borrowing it: bound MaxOutstandingMessages
// and MaxOutstandingBytes and allow MaxExtension to cover RefreshTimeout.
type API interface {
	Receive(context.Context, func(context.Context, *sdk.Message)) error
}

// Consumer refreshes configuration before requesting message acknowledgement.
type Consumer struct {
	cfg        Config
	subscriber API
}

// New validates source selection and borrows the configured subscriber.
// The application owns its lifecycle; cloud resources must already exist.
func New(cfg Config, subscriber API) (*Consumer, error) {
	if subscriber == nil {
		return nil, errors.New("fastconf/pubsub: subscriber is required")
	}
	switch cfg.Source {
	case GCS:
		if cfg.Bucket == "" || cfg.Key == "" {
			return nil, errors.New("fastconf/pubsub: bucket and key are required")
		}
	case SecretManager:
		parts := strings.Split(cfg.Secret, "/")
		var valid bool
		switch len(parts) {
		case 4: // Global secret.
			valid = parts[0] == "projects" && parts[2] == "secrets"
		case 6: // Regional secret.
			valid = parts[0] == "projects" && parts[2] == "locations" && parts[4] == "secrets"
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#") {
				valid = false
				break
			}
		}
		if !valid {
			return nil, errors.New("fastconf/pubsub: full secret resource without /versions is required")
		}
	default:
		return nil, errors.New("fastconf/pubsub: unsupported source")
	}

	if cfg.RefreshTimeout == 0 {
		cfg.RefreshTimeout = 30 * time.Second
	}
	if cfg.RefreshTimeout < 0 {
		return nil, errors.New("fastconf/pubsub: refresh timeout must be positive")
	}
	return &Consumer{cfg: cfg, subscriber: subscriber}, nil
}

// Run blocks in Subscriber.Receive. The callback refreshes synchronously before
// Ack; failures Nack for redelivery. Configure retry backoff and a dead-letter
// topic on the subscription. Receive failures are returned for supervision.
// Ack/Nack enqueue asynchronous SDK requests; their failures are not observable
// through OnError. Use SDK/server metrics and redelivery monitoring.
// Run must not be called concurrently for the same subscriber. Receive callbacks
// may overlap, so refresh and OnError must be safe for concurrent use.
// https://pkg.go.dev/cloud.google.com/go/pubsub/v2#Subscriber.Receive
func (c *Consumer) Run(ctx context.Context, refresh func(context.Context) error) error {
	if refresh == nil {
		return errors.New("fastconf/pubsub: refresh callback is required")
	}

	err := c.subscriber.Receive(ctx, func(messageCtx context.Context, msg *sdk.Message) {
		matched, err := match(c.cfg, msg)
		if err == nil && matched {
			work, cancel := context.WithTimeout(messageCtx, c.cfg.RefreshTimeout)
			err = work.Err()
			if err == nil {
				err = refresh(work)
			}
			if err == nil {
				err = work.Err()
			}
			cancel()
		}
		if err == nil {
			err = messageCtx.Err()
		}
		if err != nil {
			safe := &operationError{"process", err}
			if c.cfg.OnError != nil {
				c.cfg.OnError(safe)
			} else {
				slog.Warn("fastconf notification error", "error", safe)
			}
			msg.Nack()
			return
		}

		msg.Ack()
	})
	if err != nil {
		return &operationError{"receive", err}
	}
	return ctx.Err()
}

// Attributes are the documented notification selectors for these two services:
// https://cloud.google.com/storage/docs/pubsub-notifications
// https://cloud.google.com/secret-manager/docs/event-notifications
func match(cfg Config, msg *sdk.Message) (bool, error) {
	size := len(msg.Data)
	for k, v := range msg.Attributes {
		size += len(k) + len(v)
	}
	if size > 1<<20 {
		return false, errors.New("notification exceeds 1 MiB")
	}
	a := msg.Attributes
	if a["eventType"] == "" {
		return false, errors.New("missing notification eventType")
	}
	switch cfg.Source {
	case GCS:
		switch a["eventType"] {
		case "OBJECT_FINALIZE", "OBJECT_DELETE", "OBJECT_ARCHIVE", "OBJECT_METADATA_UPDATE":
			if a["bucketId"] == "" || a["objectId"] == "" {
				return false, errors.New("missing object identity")
			}
			return a["bucketId"] == cfg.Bucket && a["objectId"] == cfg.Key, nil
		}
	case SecretManager:
		switch a["eventType"] {
		case "SECRET_CREATE", "SECRET_UPDATE", "SECRET_DELETE", "SECRET_VERSION_ADD", "SECRET_VERSION_ENABLE", "SECRET_VERSION_DISABLE", "SECRET_VERSION_DESTROY", "SECRET_VERSION_DESTROY_SCHEDULED":
			if a["secretId"] == "" {
				return false, errors.New("missing secret identity")
			}
			return a["secretId"] == cfg.Secret, nil
		}
	}
	// Test messages, rotation requests and future event types do not prove that
	// readable configuration changed. Periodic reload remains the fallback.
	return false, nil
}

type operationError struct {
	operation string
	err       error
}

func (e *operationError) Error() string { return "fastconf/pubsub: " + e.operation + " failed" }
func (e *operationError) Unwrap() error { return e.err }

var _ API = (*sdk.Subscriber)(nil)
