package sns

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
)

// Shapes follow the AWS notification examples linked in match.go and the cloud
// notification cookbook; all identifiers and content are synthetic.
const native = `{"Records":[{"eventSource":"aws:s3","eventName":"ObjectCreated:Put","s3":{"bucket":{"name":"b"},"object":{"key":"app+%2B.json"}}}]}`
const bridge = `{"source":"aws.s3","detail-type":"Object Created","detail":{"bucket":{"name":"b"},"object":{"key":"app +.json"}}}`

func TestNotificationFormats(t *testing.T) {
	wrapped, _ := json.Marshal(map[string]string{"Type": "Notification", "TopicArn": "topic", "Message": native})
	cfg := Config{Source: S3, Bucket: "b", Key: "app +.json", TopicARN: "topic"}
	for _, body := range []string{native, bridge, string(wrapped)} {
		if ok, err := match(cfg, body); !ok || err != nil {
			t.Fatalf("match = %t, %v", ok, err)
		}
	}
	for _, body := range []string{strings.ReplaceAll(native, `"b"`, `"other"`), strings.ReplaceAll(string(wrapped), `"topic"`, `"other"`), `{"Service":"Amazon S3","Event":"s3:TestEvent"}`} {
		if ok, err := match(cfg, body); ok || err != nil {
			t.Fatalf("unrelated = %t, %v", ok, err)
		}
	}
	for _, body := range []string{"not json", "null", `{}`, `{"source":"aws.s3"}`, strings.ReplaceAll(native, "app+%2B.json", ""), strings.ReplaceAll(native, "app+%2B.json", "%zz"), strings.Repeat("x", (1<<20)+1)} {
		if _, err := match(cfg, body); err == nil {
			t.Fatal("accepted invalid message")
		}
	}
	ssm := Config{Source: ParameterStore, Path: "/app"}
	for name, want := range map[string]bool{"/app/key": true, "/application/key": false} {
		body := `{"source":"aws.ssm","detail-type":"Parameter Store Change","detail":{"name":"` + name + `","operation":"Update"}}`
		if ok, err := match(ssm, body); ok != want || err != nil {
			t.Fatalf("path %s = %t, %v", name, ok, err)
		}
	}
	app := Config{Source: AppConfig, Application: "a", Environment: "e", Profile: "p"}
	for _, event := range []string{"OnDeploymentStart", "OnDeploymentComplete", "OnDeploymentRolledBack"} {
		body := `{"Type":"` + event + `","Application":{"Id":"a"},"Environment":{"Id":"e"},"ConfigurationProfile":{"Id":"p"}}`
		if ok, err := match(app, body); !ok || err != nil {
			t.Fatalf("deployment = %t, %v", ok, err)
		}
	}
}

type fakeQueue struct {
	messages chan string
	ack      chan struct{}
	failAck  bool
	ackError error
}

func (q *fakeQueue) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	select {
	case body := <-q.messages:
		return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: &body, ReceiptHandle: aws.String("receipt")}}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (q *fakeQueue) DeleteMessageBatch(context.Context, *sqs.DeleteMessageBatchInput, ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	if q.ackError != nil {
		return nil, q.ackError
	}
	if q.failAck {
		return &sqs.DeleteMessageBatchOutput{Failed: []types.BatchResultErrorEntry{{Id: aws.String("0")}}}, nil
	}
	q.ack <- struct{}{}
	return &sqs.DeleteMessageBatchOutput{}, nil
}

func TestAckWaitsForRefreshAndReportsPartialFailure(t *testing.T) {
	for _, failure := range []string{"", "partial", "transport"} {
		q := &fakeQueue{messages: make(chan string, 1), ack: make(chan struct{}, 1), failAck: failure == "partial"}
		if failure == "transport" {
			q.ackError = errors.New("url?token=secret")
		}
		reported := make(chan error, 1)
		c, err := New(Config{QueueURL: "q", Source: S3, Bucket: "b", Key: "app +.json", OnError: func(e error) { reported <- e }}, q)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		started, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() { done <- c.Run(ctx, func(context.Context) error { close(started); <-finish; return nil }) }()
		q.messages <- native
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("refresh did not start")
		}
		select {
		case <-q.ack:
			t.Fatal("ack before refresh")
		default:
		}
		close(finish)
		if failure != "" {
			select {
			case err := <-reported:
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("leaked SDK error")
				}
				if q.ackError != nil && !errors.Is(err, q.ackError) {
					t.Fatal("lost SDK error cause")
				}
			case <-ctx.Done():
				t.Fatal("missing partial failure")
			}
		} else {
			select {
			case <-q.ack:
			case <-ctx.Done():
				t.Fatal("missing ack")
			}
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown: %v", err)
		}
	}
}

type flakyLoader struct{ calls atomic.Int32 }

func (*flakyLoader) Name() string { return "flaky" }
func (p *flakyLoader) Load(context.Context) (contracts.Snapshot, error) {
	n := p.calls.Add(1)
	if n == 2 {
		return contracts.Snapshot{}, errors.New("temporary failure")
	}
	value := "new"
	if n == 1 {
		value = "old"
	}
	return contracts.Snapshot{Map: map[string]any{"value": value}}, nil
}

func TestManagerRecoversFromRedelivery(t *testing.T) {
	q := &fakeQueue{messages: make(chan string, 1), ack: make(chan struct{}, 1)}
	reported := make(chan error, 1)
	c, _ := New(Config{QueueURL: "q", Source: S3, Bucket: "b", Key: "app +.json", OnError: func(e error) { reported <- e }}, q)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type config struct {
		Value string `json:"value"`
	}
	m, err := fastconf.New[config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(&flakyLoader{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, func(ctx context.Context) error { return m.Reload(ctx) }) }()
	q.messages <- bridge
	select {
	case <-reported:
	case <-ctx.Done():
		t.Fatal("missing refresh failure")
	}
	if m.Get().Value != "old" || len(q.ack) != 0 {
		t.Fatal("failed refresh acknowledged or published")
	}
	q.messages <- bridge // SQS redelivery, without another upstream mutation.
	select {
	case <-q.ack:
	case <-ctx.Done():
		t.Fatal("no recovery")
	}
	if m.Get().Value != "new" {
		t.Fatal("stuck on old configuration")
	}
	snapshot := m.Snapshot()
	q.messages <- bridge // Duplicate delivery must not publish a second commit.
	select {
	case <-q.ack:
	case <-ctx.Done():
		t.Fatal("duplicate not acknowledged")
	}
	if m.Snapshot() != snapshot {
		t.Fatal("duplicate published a new snapshot")
	}
	cancel()
	<-done
}

type sharedSource struct{ value atomic.Int32 }

func (*sharedSource) Name() string { return "shared" }
func (s *sharedSource) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"value": int(s.value.Load())}}, nil
}

func TestIndependentQueuesUpdateBothReplicas(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	source := &sharedSource{}
	type config struct {
		Value int `json:"value"`
	}
	var managers []*fastconf.Manager[config]
	var queues []*fakeQueue
	done := make(chan error, 2)
	for range 2 {
		m, err := fastconf.New[config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(source))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := m.Close(); err != nil {
				t.Error(err)
			}
		})
		managers = append(managers, m)
		q := &fakeQueue{messages: make(chan string, 1), ack: make(chan struct{}, 1)}
		queues = append(queues, q)
		c, err := New(Config{QueueURL: "dedicated", Source: S3, Bucket: "b", Key: "app +.json"}, q)
		if err != nil {
			t.Fatal(err)
		}
		go func() { done <- c.Run(ctx, func(work context.Context) error { return m.Reload(work) }) }()
	}
	source.value.Store(2)
	for _, q := range queues {
		q.messages <- native
	}
	for i, q := range queues {
		select {
		case <-q.ack:
			if managers[i].Get().Value != 2 {
				t.Fatal("replica remained stale")
			}
		case <-ctx.Done():
			t.Fatal("replica did not acknowledge")
		}
	}
	cancel()
	for range queues {
		<-done
	}
}

type deadlineQueue struct {
	stop      context.CancelFunc
	deadlines chan [2]time.Time
	expires   time.Time
}

func (q *deadlineQueue) ReceiveMessage(_ context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	q.expires = time.Now().Add(time.Duration(in.VisibilityTimeout) * time.Second)
	return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: aws.String(native), ReceiptHandle: aws.String("local")}}}, nil
}

func (q *deadlineQueue) DeleteMessageBatch(ctx context.Context, _ *sqs.DeleteMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	deadline, _ := ctx.Deadline()
	q.deadlines <- [2]time.Time{deadline, q.expires}
	q.stop()
	return &sqs.DeleteMessageBatchOutput{}, nil
}

func TestAckHasIndependentBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Use a separate unbounded parent so its deadline cannot conceal an
	// oversized acknowledgement budget supplied by the consumer.
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	q := &deadlineQueue{stop: stop, deadlines: make(chan [2]time.Time, 1)}
	c, err := New(Config{QueueURL: "q", Source: S3, Bucket: "b", Key: "app +.json"}, q)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx, func(context.Context) error { return nil }) }()

	select {
	case deadlines := <-q.deadlines:
		remaining := time.Until(deadlines[0])
		if remaining <= 0 || remaining > 5*time.Second {
			t.Fatalf("ACK budget = %v, want at most 5s", remaining)
		}
		if !deadlines[0].Before(deadlines[1]) {
			t.Fatal("ACK deadline exceeds visibility")
		}
	case <-ctx.Done():
		t.Fatal("message was not acknowledged")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
