package azservicebus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

const blobEvent = `{"topic":"/resource","eventType":"Microsoft.Storage.BlobCreated","subject":"/blobServices/default/containers/c/blobs/a +.json","data":{"api":"PutBlob","url":"https://account.blob.core.windows.net/c/a%20%2B.json"}}`
const configEvent = `[{"topic":"/resource","eventType":"Microsoft.AppConfiguration.KeyValueModified","data":{"key":"app:host","label":"prod"}}]`

func TestEventGridSchemasAndFilters(t *testing.T) {
	for _, tc := range []struct {
		cfg     Config
		body    string
		want    bool
		invalid bool
	}{
		{Config{Source: Blob, ResourceID: "/RESOURCE", Container: "c", Key: "a +.json"}, blobEvent, true, false},
		{Config{Source: Blob, ResourceID: "/other", Container: "c", Key: "a +.json"}, blobEvent, false, false},
		{Config{Source: Blob, ResourceID: "/resource", Container: "c", Key: "a%2F.json"}, strings.ReplaceAll(blobEvent, "a%20%2B.json", "a%252F.json"), true, false},
		{Config{Source: Blob, ResourceID: "/resource", Container: "c", Key: "a/.json"}, strings.ReplaceAll(blobEvent, "a%20%2B.json", "a%252F.json"), false, false},
		{Config{Source: Blob, ResourceID: "/resource", Container: "c", Key: "a +.json"}, strings.ReplaceAll(blobEvent, "PutBlob", "SftpCreate"), false, false},
		{Config{Source: AppConfiguration, ResourceID: "/resource", Prefix: "app:", Label: "prod"}, configEvent, true, false},
		{Config{Source: AppConfiguration, ResourceID: "/resource", Prefix: "app:"}, configEvent, false, false},
		{Config{}, `{"source":"/resource","type":"Microsoft.Storage.BlobCreated"}`, false, true},
		{Config{}, `null`, false, true},
	} {
		got, err := match(tc.cfg, []byte(tc.body))
		if got != tc.want || (err != nil) != tc.invalid {
			t.Fatalf("match = %t, %v", got, err)
		}
	}
}

type fakeReceiver struct {
	messages          chan *sdk.ReceivedMessage
	complete, abandon chan struct{}
}

func (f *fakeReceiver) ReceiveMessages(ctx context.Context, _ int, _ *sdk.ReceiveMessagesOptions) ([]*sdk.ReceivedMessage, error) {
	select {
	case msg := <-f.messages:
		return []*sdk.ReceivedMessage{msg}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *fakeReceiver) CompleteMessage(context.Context, *sdk.ReceivedMessage, *sdk.CompleteMessageOptions) error {
	f.complete <- struct{}{}
	return nil
}
func (f *fakeReceiver) AbandonMessage(context.Context, *sdk.ReceivedMessage, *sdk.AbandonMessageOptions) error {
	f.abandon <- struct{}{}
	return nil
}

func TestSettlementAndLockDeadline(t *testing.T) {
	for _, fail := range []bool{false, true} {
		r := &fakeReceiver{make(chan *sdk.ReceivedMessage, 1), make(chan struct{}, 1), make(chan struct{}, 1)}
		c, err := New(Config{Source: Blob, ResourceID: "/resource", Container: "c", Key: "a +.json", OnError: func(error) {}}, r)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		started, finish, done := make(chan time.Time, 1), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- c.Run(ctx, func(work context.Context) error {
				deadline, _ := work.Deadline()
				started <- deadline
				<-finish
				if fail {
					return errors.New("failure")
				}
				return nil
			})
		}()
		lock := time.Now().Add(1500 * time.Millisecond)
		r.messages <- &sdk.ReceivedMessage{Body: []byte(blobEvent), LockedUntil: &lock}
		select {
		case deadline := <-started:
			if !deadline.Before(lock) {
				t.Fatal("deadline exceeds lock")
			}
		case <-ctx.Done():
			t.Fatal("no refresh")
		}
		if len(r.complete) != 0 {
			t.Fatal("completed before refresh")
		}
		close(finish)
		settled := r.complete
		if fail {
			settled = r.abandon
		}
		select {
		case <-settled:
		case <-ctx.Done():
			t.Fatal("not settled")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestExpiredLockDoesNotRefresh(t *testing.T) {
	r := &fakeReceiver{make(chan *sdk.ReceivedMessage, 1), make(chan struct{}, 1), make(chan struct{}, 1)}
	c, _ := New(Config{Source: Blob, ResourceID: "/resource", Container: "c", Key: "a +.json", OnError: func(error) {}}, r)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, func(context.Context) error { panic("must not refresh with expired lock") })
	}()
	lock := time.Now().Add(-time.Second)
	r.messages <- &sdk.ReceivedMessage{Body: []byte(blobEvent), LockedUntil: &lock}
	select {
	case <-r.abandon:
	case <-ctx.Done():
		t.Fatal("no abandon")
	}
	cancel()
	<-done
}
