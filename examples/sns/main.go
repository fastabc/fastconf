//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"testing/fstest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/integrations/notify/sns"
)

// The in-memory source and SQS double make the lifecycle runnable without AWS.
// Production uses a cloud provider and an application-configured *sqs.Client.
type source struct{ value string }

func (*source) Name() string { return "example" }
func (s *source) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: map[string]any{"value": s.value}}, nil
}

type queue struct {
	delivered bool
	stop      context.CancelFunc
}

func (q *queue) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if q.delivered {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	q.delivered = true
	body := `{"Type":"Notification","TopicArn":"example","Message":"{\"Records\":[{\"eventSource\":\"aws:s3\",\"eventName\":\"ObjectCreated:Put\",\"s3\":{\"bucket\":{\"name\":\"configs\"},\"object\":{\"key\":\"app.json\"}}}]}"}`
	return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: &body, ReceiptHandle: aws.String("local")}}}, nil
}

func (q *queue) DeleteMessageBatch(context.Context, *sqs.DeleteMessageBatchInput, ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	fmt.Println("ack after reload")
	q.stop()
	return &sqs.DeleteMessageBatchOutput{}, nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Load the initial snapshot before starting the notification consumer.
	s := &source{value: "old"}
	type config struct {
		Value string `json:"value"`
	}
	m, err := fastconf.New[config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(s))
	if err != nil {
		panic(err)
	}
	defer m.Close()

	// Change the source once; the queue delivers the corresponding S3 notification.
	s.value = "new"
	c, err := sns.New(sns.Config{
		QueueURL: "local", TopicARN: "example",
		Source: sns.S3, Bucket: "configs", Key: "app.json",
	}, &queue{stop: cancel})
	if err != nil {
		panic(err)
	}

	// Return Reload errors so the consumer only acknowledges successful refreshes.
	if err = c.Run(ctx, func(work context.Context) error { return m.Reload(work) }); err != nil && !errors.Is(err, context.Canceled) {
		panic(err)
	}

	fmt.Println("configuration:", m.Get().Value)

	// See the cloud-notifications cookbook for periodic reconciliation and
	// supervision. Those remain necessary when using real queues.
}
