//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	sdk "cloud.google.com/go/pubsub/v2"
	pb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/fastabc/fastconf/integrations/notify/pubsub"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

// A local gRPC broker runs the real Pub/Sub SDK without credentials.
type broker struct {
	pb.UnimplementedSubscriberServer
	stop context.CancelFunc
}

func (b *broker) StreamingPull(stream pb.Subscriber_StreamingPullServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}

	// Deliver one object event; settlement arrives through the unary ACK RPC.
	response := &pb.StreamingPullResponse{ReceivedMessages: []*pb.ReceivedMessage{{
		AckId: "receipt",
		Message: &pb.PubsubMessage{
			MessageId: "event",
			Attributes: map[string]string{
				"eventType": "OBJECT_FINALIZE", "bucketId": "configs", "objectId": "app.json",
			},
		},
	}}}
	if err := stream.Send(response); err != nil {
		return err
	}

	// Keep the stream open until the consumer or server shuts down.
	for {
		if _, err := stream.Recv(); err != nil {
			return err
		}
	}
}

func (b *broker) Acknowledge(context.Context, *pb.AcknowledgeRequest) (*emptypb.Empty, error) {
	fmt.Println("ack after refresh")
	b.stop()
	return &emptypb.Empty{}, nil
}

func (*broker) ModifyAckDeadline(context.Context, *pb.ModifyAckDeadlineRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start a local peer for the real Pub/Sub SDK.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := grpc.NewServer()
	pb.RegisterSubscriberServer(server, &broker{stop: cancel})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	// Production supplies an authenticated client; this connection stays local.
	client, err := sdk.NewClient(ctx, "local",
		option.WithEndpoint(listener.Addr().String()),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Bound concurrency and lease extension for the synchronous refresh callback.
	sub := client.Subscriber("config")
	sub.ReceiveSettings.NumGoroutines = 1
	sub.ReceiveSettings.MaxOutstandingMessages = 1
	sub.ReceiveSettings.MaxExtension = time.Minute

	// Filter by the same bucket and full object key as the GCS provider.
	c, err := pubsub.New(pubsub.Config{Source: pubsub.GCS, Bucket: "configs", Key: "app.json"}, sub)
	if err != nil {
		panic(err)
	}

	err = c.Run(ctx, func(context.Context) error {
		// Use mgr.Reload(ctx) here and return its error to control Ack/Nack.
		fmt.Println("refresh successful")
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		panic(err)
	}
}
