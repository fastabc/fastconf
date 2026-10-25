//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdk "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/fastabc/fastconf/integrations/notify/azservicebus"
)

// Production borrows an SDK receiver from a queue or topic subscription opened
// in PeekLock mode. This double demonstrates settlement without an Azure account.
type receiver struct {
	delivered bool
	stop      context.CancelFunc
}

func (r *receiver) ReceiveMessages(ctx context.Context, _ int, _ *sdk.ReceiveMessagesOptions) ([]*sdk.ReceivedMessage, error) {
	if r.delivered {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r.delivered = true
	lock := time.Now().Add(time.Minute)
	return []*sdk.ReceivedMessage{{LockedUntil: &lock, Body: []byte(`{"topic":"/example","eventType":"Microsoft.AppConfiguration.KeyValueModified","data":{"key":"app:host","label":"prod"}}`)}}, nil
}

func (r *receiver) CompleteMessage(context.Context, *sdk.ReceivedMessage, *sdk.CompleteMessageOptions) error {
	fmt.Println("complete after refresh")
	r.stop()
	return nil
}

func (*receiver) AbandonMessage(context.Context, *sdk.ReceivedMessage, *sdk.AbandonMessageOptions) error {
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Match one App Configuration store and the same prefix/label as its provider.
	c, err := azservicebus.New(azservicebus.Config{
		Source: azservicebus.AppConfiguration, ResourceID: "/example",
		Prefix: "app:", Label: "prod",
	}, &receiver{stop: cancel})
	if err != nil {
		panic(err)
	}

	// Complete runs only after this callback succeeds, within the message lock.
	err = c.Run(ctx, func(context.Context) error {
		// Use mgr.Reload(ctx) here; return its error without swallowing it.
		fmt.Println("refresh successful")
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		panic(err)
	}
}
