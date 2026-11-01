package s3events

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/fastabc/fastconf/contracts"
)

type receiveFailure struct {
	API
	err error
}

func (f *receiveFailure) ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return &sqs.ReceiveMessageOutput{}, f.err
}

func TestPoll_ReceiveErrorsAreLoggedOncePerOutage(t *testing.T) {
	f := &receiveFailure{err: errors.New("AccessDenied: secret-arn-detail")}
	var logs bytes.Buffer
	p := &Provider{queueURL: "q", client: f, maxMsgs: 1, retryDelay: time.Millisecond, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	gap, failing := false, false
	for range 3 {
		p.poll(context.Background(), make(chan contracts.Event, 1), &gap, &failing)
	}
	if strings.Count(logs.String(), "receive failed; retrying") != 1 || strings.Contains(logs.String(), "secret-arn-detail") {
		t.Fatalf("logs=%q", logs.String())
	}
	f.err = nil
	p.poll(context.Background(), make(chan contracts.Event, 1), &gap, &failing)
	f.err = errors.New("again")
	p.poll(context.Background(), make(chan contracts.Event, 1), &gap, &failing)
	if strings.Count(logs.String(), "receive failed; retrying") != 2 {
		t.Fatalf("logs after recovery=%q", logs.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failing = false
	p.poll(ctx, make(chan contracts.Event, 1), &gap, &failing)
	if strings.Count(logs.String(), "receive failed; retrying") != 2 {
		t.Fatal("cancellation logged as outage")
	}
}

// Provider was comparable in v1; private fields must preserve that property.
var _ = map[Provider]struct{}{}
