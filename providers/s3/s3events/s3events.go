// Package s3events turns matching S3 EventBridge/SQS messages into reload triggers.
// Load contributes an empty map; pair with providers/s3 for data. Unrelated messages
// are left in the queue; malformed/unknown envelopes are ignored. Receive failures
// emit one sanitized warning per outage.
//
// Watch deletes accepted messages after delivery, before Manager reload completes.
// It is best-effort: retain periodic Reload for recovery from missed or failed refreshes.
// See docs/cookbook/sources/cloud-notifications.md for migration.
//
// Deprecated: Use integrations/notify/sns for refresh-before-ack delivery.
package s3events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/fastabc/fastconf/contracts"
)

// Config holds all settings for the S3-events watch provider.
type Config struct {
	Name      string // Provider name (default "s3events")
	QueueURL  string // SQS queue URL (required)
	Bucket    string // S3 bucket to filter events for (required)
	KeyPrefix string // Optional S3 key prefix filter; empty matches all keys in bucket
	Region    string // AWS region (required by New)
	AccessKey string // AWS access key (required by New)
	SecretKey string // AWS secret key (required by New)
	Token     string // Optional STS session token
	Endpoint  string // Custom SQS endpoint (LocalStack); empty uses AWS default

	// MaxMessages is the SQS ReceiveMessage batch size (1..10). Default
	// 10. The provider only ACKs (deletes) messages it actually emits.
	MaxMessages int32
	// WaitTimeSeconds is the SQS long-poll duration (0..20). Default 20.
	WaitTimeSeconds int32
	// Priority is the provider's merge priority. Default
	// contracts.PriorityKV (30). Because this provider contributes an
	// empty map, the priority only matters if you swap in a non-empty
	// Load by wrapping the Provider.
	Priority int
}

// API is the minimal subset of *sqs.Client the provider uses. Tests
// substitute an in-memory implementation.
type API interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, in *sqs.DeleteMessageBatchInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error)
}

// Provider implements contracts.Provider. It is watch-only: Load
// returns an empty map; Watch fans matched SQS messages into a
// contracts.Event channel.
type Provider struct {
	name       string
	queueURL   string
	bucket     string
	keyPrefix  string
	priority   int
	maxMsgs    int32
	waitSecs   int32
	client     API
	logger     *slog.Logger
	retryDelay time.Duration
}

// New builds a real *sqs.Client using static credentials and
// constructs the provider. Use NewWithClient for an application-configured
// client, including one using workload identity or refreshing credentials.
func New(cfg Config) (*Provider, error) {
	if cfg.Region == "" {
		return nil, errors.New("fastconf/s3events: region is required")
	}
	if cfg.AccessKey == "" {
		return nil, errors.New("fastconf/s3events: access key is required")
	}
	if cfg.SecretKey == "" {
		return nil, errors.New("fastconf/s3events: secret key is required")
	}
	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, cfg.Token,
		),
	}
	client := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return newProvider(cfg, client)
}

// NewWithClient accepts an externally constructed SQS client.
// Production code can inject an SDK client with refreshing credentials and
// application-selected retry/transport settings. The caller owns the client.
func NewWithClient(cfg Config, client API) (*Provider, error) {
	if client == nil {
		return nil, errors.New("fastconf/s3events: client is required")
	}
	return newProvider(cfg, client)
}

func newProvider(cfg Config, client API) (*Provider, error) {
	if cfg.QueueURL == "" {
		return nil, errors.New("fastconf/s3events: queue url is required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("fastconf/s3events: bucket is required")
	}
	name := cfg.Name
	if name == "" {
		name = "s3events:" + cfg.Bucket
		if cfg.KeyPrefix != "" {
			name += "/" + cfg.KeyPrefix
		}
	}
	maxMsgs := cfg.MaxMessages
	if maxMsgs <= 0 || maxMsgs > 10 {
		maxMsgs = 10
	}
	waitSecs := cfg.WaitTimeSeconds
	if waitSecs <= 0 || waitSecs > 20 {
		waitSecs = 20
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = contracts.PriorityKV
	}
	return &Provider{
		name:      name,
		queueURL:  cfg.QueueURL,
		bucket:    cfg.Bucket,
		keyPrefix: cfg.KeyPrefix,
		priority:  priority,
		maxMsgs:   maxMsgs,
		waitSecs:  waitSecs,
		client:    client,
		logger:    slog.Default(),
	}, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load implements contracts.Provider. The watch-only provider
// contributes no configuration of its own — pair with providers/s3 for
// the actual data. Returning an empty map (rather than nil) ensures
// downstream stages always receive a non-nil layer.
func (p *Provider) Load(_ context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{}}, nil
}

// Watch implements contracts.Watcher. The returned channel is closed
// when ctx is done. Events arrive at most once per matched SQS message
// before ACK (deletion); failed ACKs may cause redelivery. A non-empty
// from cannot be resumed, so the first event reports Gap.
func (p *Provider) Watch(ctx context.Context, from string) (<-chan contracts.Event, error) {
	out := make(chan contracts.Event, 16)
	go func() {
		defer close(out)
		gap := from != ""
		failing := false
		for {
			if err := ctx.Err(); err != nil {
				return
			}
			p.poll(ctx, out, &gap, &failing)
		}
	}()
	return out, nil
}

// poll executes one ReceiveMessage cycle and forwards matching events
// to out. Each receive outage emits one sanitized warning.
func (p *Provider) poll(ctx context.Context, out chan<- contracts.Event, gap, failing *bool) {
	in := &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(p.queueURL),
		MaxNumberOfMessages: p.maxMsgs,
		WaitTimeSeconds:     p.waitSecs,
	}
	resp, err := p.client.ReceiveMessage(ctx, in)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if !*failing {
			*failing = true
			p.logger.Warn("fastconf/s3events: receive failed; retrying", "queue", p.queueURL)
		}
		delay := p.retryDelay
		if delay <= 0 {
			delay = time.Second
		}
		// Back off briefly so a misconfigured queue does not pin a CPU.
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
		return
	}
	*failing = false
	var matched []sqstypes.Message
	for _, msg := range resp.Messages {
		ev, ok := p.decodeAndMatch(msg)
		if !ok {
			continue
		}
		ev.Gap = *gap
		select {
		case out <- ev:
			*gap = false
		case <-ctx.Done():
			return
		}
		matched = append(matched, msg)
	}
	if len(matched) > 0 {
		p.ack(ctx, matched)
	}
}

// decodeAndMatch parses an SQS message body as an EventBridge S3
// Object-Created envelope and decides whether it concerns the bucket /
// key prefix this provider was configured for.
func (p *Provider) decodeAndMatch(msg sqstypes.Message) (contracts.Event, bool) {
	body := aws.ToString(msg.Body)
	if body == "" {
		return contracts.Event{}, false
	}
	var env eventBridgeEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return contracts.Event{}, false
	}
	if env.Source != "aws.s3" {
		return contracts.Event{}, false
	}
	if !isObjectMutation(env.DetailType) {
		return contracts.Event{}, false
	}
	if env.Detail.Bucket.Name != p.bucket {
		return contracts.Event{}, false
	}
	if p.keyPrefix != "" && !strings.HasPrefix(env.Detail.Object.Key, p.keyPrefix) {
		return contracts.Event{}, false
	}
	at := time.Now()
	if env.Time != "" {
		if t, err := time.Parse(time.RFC3339, env.Time); err == nil {
			at = t
		}
	}
	return contracts.Event{
		Source:   p.name,
		Reason:   "s3:" + env.DetailType + ":" + env.Detail.Object.Key,
		Revision: env.Detail.Object.ETag, // empty for delete events; that's fine
		At:       at,
	}, true
}

// ack deletes the matched messages from SQS in a single batch call.
// Failures are dropped; SQS visibility timeout will redeliver and the
// next poll will re-emit the event (Manager dedupes on Hash, so a
// duplicate reload is a no-op).
func (p *Provider) ack(ctx context.Context, matched []sqstypes.Message) {
	if len(matched) == 0 {
		return
	}
	entries := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(matched))
	for i, m := range matched {
		entries = append(entries, sqstypes.DeleteMessageBatchRequestEntry{
			Id:            aws.String(fmt.Sprintf("%d", i)),
			ReceiptHandle: m.ReceiptHandle,
		})
	}
	_, _ = p.client.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
		QueueUrl: aws.String(p.queueURL),
		Entries:  entries,
	})
}

// eventBridgeEnvelope is the subset of the EventBridge S3
// notification schema this provider cares about. See the AWS docs at
// "Amazon EventBridge — S3 events" for the full shape.
type eventBridgeEnvelope struct {
	Source     string `json:"source"`
	DetailType string `json:"detail-type"`
	Time       string `json:"time"`
	Detail     struct {
		Bucket struct {
			Name string `json:"name"`
		} `json:"bucket"`
		Object struct {
			Key  string `json:"key"`
			ETag string `json:"etag"`
		} `json:"object"`
	} `json:"detail"`
}

// isObjectMutation classifies detail-type values that should drive a
// reload. EventBridge sends specific event names; we accept any that
// indicate the object changed.
func isObjectMutation(detailType string) bool {
	switch detailType {
	case
		"Object Created",
		"Object Deleted",
		"Object Restore Completed",
		"Object Tags Added",
		"Object Tags Deleted",
		"Object ACL Updated":
		return true
	}
	return false
}

// Compile-time interface assertion.
var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
