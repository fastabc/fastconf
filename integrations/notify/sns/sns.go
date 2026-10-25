// Package sns consumes configuration notifications delivered through SQS.
// It owns neither the queue nor the SDK client. Each independent configuration
// replica needs its own queue, or a separate broadcast mechanism.
package sns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Source selects the service's notification schema.
type Source string

const (
	S3             Source = "s3"
	AppConfig      Source = "appconfig"
	ParameterStore Source = "parameterstore"
)

// Config selects one configuration source. The queue must be dedicated to this
// consumer. TopicARN validates SNS envelopes; raw delivery instead relies on the
// queue's publisher policy. Source identifiers are exact, except Path which
// selects a Parameter Store hierarchy on slash boundaries.
type Config struct {
	QueueURL string
	TopicARN string
	Source   Source

	Bucket string // Required for S3.
	Key    string // Required for S3; exact object key.

	Application string // Required for AppConfig.
	Environment string // Required for AppConfig.
	Profile     string // Required for AppConfig.

	Path string // Required for ParameterStore; slash-delimited hierarchy.

	// RefreshTimeout defaults to 30s. Refresh must honor cancellation.
	RefreshTimeout time.Duration
	// OnError observes safe error messages, with causes available via errors.As.
	// Nil logs warnings through slog.Default. It must not block or panic.
	OnError func(error)
}

// API is implemented by *sqs.Client. The caller configures SDK authentication,
// retries and transport timeouts (long polls wait up to 20 seconds).
type API interface {
	ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(context.Context, *sqs.DeleteMessageBatchInput, ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error)
}

// Acknowledgement has its own budget, leaving another five seconds of the
// visibility margin for transport overhead. It must not reuse RefreshTimeout.
const ackTimeout = 5 * time.Second

// Consumer synchronously refreshes before deleting an accepted message.
type Consumer struct {
	cfg    Config
	client API
}

// New validates source selection. Cloud resources must already exist.
func New(cfg Config, client API) (*Consumer, error) {
	if client == nil || cfg.QueueURL == "" {
		return nil, errors.New("fastconf/sns: client and queue URL are required")
	}
	if err := validateSource(cfg); err != nil {
		return nil, err
	}

	if cfg.RefreshTimeout == 0 {
		cfg.RefreshTimeout = 30 * time.Second
	}
	if cfg.RefreshTimeout < 0 || cfg.RefreshTimeout > 12*time.Hour-10*time.Second {
		return nil, errors.New("fastconf/sns: refresh timeout must be positive and below 12h minus 10s")
	}
	return &Consumer{cfg: cfg, client: client}, nil
}

// Run blocks until canceled or receiving fails after the SDK's retries. A
// receive error is returned for application supervision. Refresh/decode/delete
// failures are reported and left for redelivery and the queue's redrive policy.
// Configure a dead-letter queue and periodic full reloads for missed events.
// Run must not be called concurrently on the same consumer. Acknowledgement
// has a separate five-second timeout within the ten-second visibility margin.
func (c *Consumer) Run(ctx context.Context, refresh func(context.Context) error) error {
	if refresh == nil {
		return errors.New("fastconf/sns: refresh callback is required")
	}

	visibility := int32((c.cfg.RefreshTimeout+time.Second-1)/time.Second) + 10
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(c.cfg.QueueURL), MaxNumberOfMessages: 1,
			WaitTimeSeconds: 20, VisibilityTimeout: visibility,
		})
		if err != nil {
			return &operationError{"receive", err}
		}

		for _, msg := range out.Messages {
			matched, err := match(c.cfg, aws.ToString(msg.Body))
			if err == nil && matched {
				work, cancel := context.WithTimeout(ctx, c.cfg.RefreshTimeout)
				err = refresh(work)
				if err == nil {
					err = work.Err()
				}
				cancel()
			}
			if err != nil {
				c.report("process", err)
				continue
			}

			if err := ctx.Err(); err != nil {
				return err
			}

			// Well-formed unrelated events are acknowledged on this dedicated queue.
			ackCtx, cancel := context.WithTimeout(ctx, ackTimeout)
			deleted, err := c.client.DeleteMessageBatch(ackCtx, &sqs.DeleteMessageBatchInput{
				QueueUrl: aws.String(c.cfg.QueueURL),
				Entries:  []types.DeleteMessageBatchRequestEntry{{Id: aws.String("0"), ReceiptHandle: msg.ReceiptHandle}},
			})
			cancel()
			if err == nil && len(deleted.Failed) != 0 {
				err = fmt.Errorf("%d message deletions failed", len(deleted.Failed))
			}
			if err != nil {
				c.report("acknowledge", err)
			}
		}
	}
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

func (e *operationError) Error() string { return "fastconf/sns: " + e.operation + " failed" }
func (e *operationError) Unwrap() error { return e.err }
