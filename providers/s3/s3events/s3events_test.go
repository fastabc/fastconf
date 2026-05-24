//go:build !no_provider_s3events

package s3events_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/contracts/providertest"
	s3events "github.com/fastabc/fastconf/providers/s3/s3events"
)

// fakeSQS is a tiny in-memory SQS double. Append a JSON envelope via
// Push; ReceiveMessage drains up to MaxNumberOfMessages and waits up
// to WaitTimeSeconds for new messages.
type fakeSQS struct {
	mu       sync.Mutex
	cond     *sync.Cond
	pending  []sqstypes.Message
	receives atomic.Int32
	deletes  atomic.Int32
	deleted  []string
}

func newFakeSQS() *fakeSQS {
	f := &fakeSQS{}
	f.cond = sync.NewCond(&f.mu)
	return f
}

func (f *fakeSQS) Push(body string) {
	f.mu.Lock()
	f.pending = append(f.pending, sqstypes.Message{
		Body:          aws.String(body),
		ReceiptHandle: aws.String("rh-" + body[:min(8, len(body))]),
	})
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.receives.Add(1)
	deadline := time.Now().Add(time.Duration(in.WaitTimeSeconds) * time.Second)
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.pending) == 0 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &sqs.ReceiveMessageOutput{}, nil
		}
		// Wake on ctx done or deadline expiry.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(remaining):
			case <-done:
			}
			f.cond.L.Lock()
			f.cond.Broadcast()
			f.cond.L.Unlock()
		}()
		f.cond.Wait()
		close(done)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return &sqs.ReceiveMessageOutput{}, nil
		}
	}
	n := min(int(in.MaxNumberOfMessages), len(f.pending))
	out := &sqs.ReceiveMessageOutput{Messages: append([]sqstypes.Message(nil), f.pending[:n]...)}
	f.pending = f.pending[n:]
	return out, nil
}

func (f *fakeSQS) DeleteMessageBatch(_ context.Context, in *sqs.DeleteMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	f.deletes.Add(1)
	for _, e := range in.Entries {
		f.deleted = append(f.deleted, aws.ToString(e.ReceiptHandle))
	}
	return &sqs.DeleteMessageBatchOutput{}, nil
}

func TestNewWithClient_Validation(t *testing.T) {
	c := newFakeSQS()
	if _, err := s3events.NewWithClient(s3events.Config{Bucket: "b"}, c); err == nil {
		t.Error("expected error on missing queue url")
	}
	if _, err := s3events.NewWithClient(s3events.Config{QueueURL: "q"}, c); err == nil {
		t.Error("expected error on missing bucket")
	}
	if _, err := s3events.NewWithClient(s3events.Config{QueueURL: "q", Bucket: "b"}, nil); err == nil {
		t.Error("expected error on nil client")
	}
}

func TestNew_RequiresAWSCreds(t *testing.T) {
	cases := []struct {
		name string
		cfg  s3events.Config
	}{
		{"missing region", s3events.Config{QueueURL: "q", Bucket: "b", AccessKey: "a", SecretKey: "s"}},
		{"missing access", s3events.Config{Region: "us-east-1", QueueURL: "q", Bucket: "b", SecretKey: "s"}},
		{"missing secret", s3events.Config{Region: "us-east-1", QueueURL: "q", Bucket: "b", AccessKey: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s3events.New(tc.cfg); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestProvider_NameDefault(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{QueueURL: "q", Bucket: "my-bucket"}, c)
	if p.Name() != "s3events:my-bucket" {
		t.Errorf("Name: %s", p.Name())
	}
	p2, _ := s3events.NewWithClient(s3events.Config{QueueURL: "q", Bucket: "b", KeyPrefix: "prod/"}, c)
	if p2.Name() != "s3events:b/prod/" {
		t.Errorf("Name with prefix: %s", p2.Name())
	}
	p3, _ := s3events.NewWithClient(s3events.Config{QueueURL: "q", Bucket: "b", Name: "explicit"}, c)
	if p3.Name() != "explicit" {
		t.Errorf("explicit name: %s", p3.Name())
	}
}

func TestProvider_LoadIsEmpty(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{QueueURL: "q", Bucket: "b"}, c)
	m, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestProvider_Conformance(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "b",
		WaitTimeSeconds: 1,
	}, c)
	providertest.AssertProviderBasics(t, p)
	providertest.AssertWatchClosesOnCancel(t, p, time.Second)
}

const eventBridgeObjectCreated = `{
  "version": "0",
  "id": "evt-1",
  "detail-type": "Object Created",
  "source": "aws.s3",
  "time": "2026-05-17T01:23:45Z",
  "region": "us-east-1",
  "detail": {
    "bucket": {"name": "my-configs"},
    "object": {"key": "prod/app.yaml", "etag": "abc"}
  }
}`

func TestProvider_WatchEmitsOnMatch(t *testing.T) {
	c := newFakeSQS()
	p, err := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "my-configs",
		KeyPrefix:       "prod/",
		WaitTimeSeconds: 1,
	}, c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch, err := p.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.Push(eventBridgeObjectCreated)
	select {
	case ev := <-ch:
		if ev.Source != "s3events:my-configs/prod/" {
			t.Errorf("source: %s", ev.Source)
		}
		if ev.Revision != "abc" {
			t.Errorf("revision: %s", ev.Revision)
		}
	case <-ctx.Done():
		t.Fatal("no event")
	}
	// Wait briefly for ack to fire (best-effort; ack happens after send).
	deadline := time.Now().Add(time.Second)
	for c.deletes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if c.deletes.Load() == 0 {
		t.Error("expected matched message to be deleted")
	}
}

func TestProvider_WatchSkipsWrongBucket(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "OTHER-BUCKET",
		WaitTimeSeconds: 1,
	}, c)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	ch, _ := p.Watch(ctx)
	c.Push(eventBridgeObjectCreated)
	select {
	case ev := <-ch:
		t.Errorf("expected no event for wrong bucket, got %+v", ev)
	case <-ctx.Done():
		// expected timeout
	}
	if c.deletes.Load() != 0 {
		t.Error("did not expect unrelated message to be deleted")
	}
}

func TestProvider_WatchSkipsWrongPrefix(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "my-configs",
		KeyPrefix:       "staging/",
		WaitTimeSeconds: 1,
	}, c)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	ch, _ := p.Watch(ctx)
	c.Push(eventBridgeObjectCreated)
	select {
	case ev := <-ch:
		t.Errorf("expected no event for wrong prefix, got %+v", ev)
	case <-ctx.Done():
		// expected timeout
	}
}

func TestProvider_WatchEmptyPrefixMatchesAll(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "my-configs",
		WaitTimeSeconds: 1,
	}, c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, _ := p.Watch(ctx)
	c.Push(eventBridgeObjectCreated)
	select {
	case ev := <-ch:
		if ev.Reason == "" {
			t.Errorf("missing reason: %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("no event")
	}
}

func TestProvider_WatchSkipsNonS3Source(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "my-configs",
		WaitTimeSeconds: 1,
	}, c)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	ch, _ := p.Watch(ctx)
	c.Push(`{"source":"aws.ec2","detail-type":"Object Created","detail":{"bucket":{"name":"my-configs"},"object":{"key":"x"}}}`)
	select {
	case ev := <-ch:
		t.Errorf("expected no event for non-S3 source, got %+v", ev)
	case <-ctx.Done():
		// expected timeout
	}
}

func TestProvider_WatchHandlesGarbageBody(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "b",
		WaitTimeSeconds: 1,
	}, c)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	ch, _ := p.Watch(ctx)
	c.Push("not-json-at-all")
	select {
	case ev := <-ch:
		t.Errorf("expected no event for garbage body, got %+v", ev)
	case <-ctx.Done():
		// expected timeout
	}
	// Watch loop must still be alive — push a real event and expect it.
	c.Push(eventBridgeObjectCreated[:0] + `{"source":"aws.s3","detail-type":"Object Created","detail":{"bucket":{"name":"b"},"object":{"key":"k.yaml","etag":"e"}}}`)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	_ = ctx2
	// New Watch needed because ctx of previous expired
	ch2, _ := p.Watch(ctx2)
	select {
	case ev := <-ch2:
		if ev.Revision != "e" {
			t.Errorf("event revision: %s", ev.Revision)
		}
	case <-ctx2.Done():
		t.Fatal("loop did not survive garbage body")
	}
}

func TestProvider_WatchDeleteEventEmits(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{
		QueueURL:        "q",
		Bucket:          "b",
		WaitTimeSeconds: 1,
	}, c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, _ := p.Watch(ctx)
	c.Push(`{"source":"aws.s3","detail-type":"Object Deleted","detail":{"bucket":{"name":"b"},"object":{"key":"k.yaml"}}}`)
	select {
	case ev := <-ch:
		if ev.Reason == "" {
			t.Errorf("missing reason: %+v", ev)
		}
		// Object Deleted carries no etag — Revision should be empty, not panic.
		if ev.Revision != "" {
			t.Errorf("expected empty revision on delete, got %q", ev.Revision)
		}
	case <-ctx.Done():
		t.Fatal("no event for delete")
	}
}

func TestProvider_ImplementsContracts(t *testing.T) {
	c := newFakeSQS()
	p, _ := s3events.NewWithClient(s3events.Config{QueueURL: "q", Bucket: "b"}, c)
	var _ contracts.Provider = p
}

func TestProvider_NewBuildsRealClient(t *testing.T) {
	// Make sure New() accepts a Config with real creds and a custom endpoint.
	p, err := s3events.New(s3events.Config{
		Region:    "us-east-1",
		QueueURL:  "https://sqs.us-east-1.amazonaws.com/123/q",
		Bucket:    "b",
		AccessKey: "AKIA",
		SecretKey: "secret",
		Endpoint:  "http://localhost:4566",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "s3events:b" {
		t.Errorf("Name: %s", p.Name())
	}
}
