package pubsub

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	sdk "cloud.google.com/go/pubsub/v2"
	pb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestNewValidatesSelection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   Config
		valid bool
	}{
		{"object", Config{Source: GCS, Bucket: "b", Key: "app.json"}, true},
		{"global secret", Config{Source: SecretManager, Secret: "projects/p/secrets/s"}, true},
		{"regional secret", Config{Source: SecretManager, Secret: "projects/p/locations/l/secrets/s"}, true},
		{"unknown source", Config{Source: "other"}, false},
		{"missing bucket", Config{Source: GCS, Key: "app.json"}, false},
		{"missing key", Config{Source: GCS, Bucket: "b"}, false},
		{"empty project", Config{Source: SecretManager, Secret: "projects//secrets/s"}, false},
		{"empty secret", Config{Source: SecretManager, Secret: "projects/p/secrets/"}, false},
		{"empty location", Config{Source: SecretManager, Secret: "projects/p/locations//secrets/s"}, false},
		{"version suffix", Config{Source: SecretManager, Secret: "projects/p/secrets/s/versions/latest"}, false},
		{"unknown segment", Config{Source: SecretManager, Secret: "projects/p/other/l/secrets/s"}, false},
		{"parent segment", Config{Source: SecretManager, Secret: "projects/p/secrets/.."}, false},
		{"query", Config{Source: SecretManager, Secret: "projects/p/secrets/s?version=1"}, false},
		{"negative timeout", Config{Source: GCS, Bucket: "b", Key: "app.json", RefreshTimeout: -time.Second}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			consumer, err := New(tc.cfg, &sdk.Subscriber{})
			if (err == nil) != tc.valid {
				t.Fatalf("New error = %v, valid = %t", err, tc.valid)
			}
			if tc.valid && consumer.cfg.RefreshTimeout != 30*time.Second {
				t.Fatalf("default refresh timeout = %s", consumer.cfg.RefreshTimeout)
			}
		})
	}

	if _, err := New(Config{Source: GCS, Bucket: "b", Key: "app.json"}, nil); err == nil {
		t.Fatal("accepted a nil subscriber")
	}
}

func TestSourceFilters(t *testing.T) {
	gcs := Config{Source: GCS, Bucket: "b", Key: "app.json"}
	secret := Config{Source: SecretManager, Secret: "projects/p/secrets/s"}
	for _, tc := range []struct {
		cfg     Config
		attrs   map[string]string
		want    bool
		invalid bool
	}{
		{gcs, map[string]string{"eventType": "OBJECT_FINALIZE", "bucketId": "b", "objectId": "app.json", "objectGeneration": "2"}, true, false},
		{gcs, map[string]string{"eventType": "OBJECT_DELETE", "bucketId": "other", "objectId": "app.json"}, false, false},
		{secret, map[string]string{"eventType": "SECRET_VERSION_DISABLE", "secretId": "projects/p/secrets/s"}, true, false},
		{secret, map[string]string{"eventType": "TOPIC_CONFIGURED"}, false, false},
		{secret, map[string]string{"eventType": "SECRET_ROTATE", "secretId": "projects/p/secrets/s"}, false, false},
		{gcs, map[string]string{"eventType": "SECRET_VERSION_ADD", "secretId": "projects/p/secrets/s"}, false, false},
		{secret, map[string]string{}, false, true},
	} {
		ok, err := match(tc.cfg, &sdk.Message{Attributes: tc.attrs})
		if ok != tc.want || (err != nil) != tc.invalid {
			t.Fatalf("match = %t %v", ok, err)
		}
	}
}

// A real local gRPC peer exercises SDK Ack/Nack batching rather than replacing
// Message with a value whose unexported acknowledgement handler is nil.
type broker struct {
	pb.UnimplementedSubscriberServer
	settled chan bool
}

func (b *broker) Acknowledge(context.Context, *pb.AcknowledgeRequest) (*emptypb.Empty, error) {
	b.settled <- true
	return &emptypb.Empty{}, nil
}

func (b *broker) ModifyAckDeadline(_ context.Context, req *pb.ModifyAckDeadlineRequest) (*emptypb.Empty, error) {
	if req.AckDeadlineSeconds == 0 {
		b.settled <- false
	}
	return &emptypb.Empty{}, nil
}

func (b *broker) StreamingPull(stream pb.Subscriber_StreamingPullServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&pb.StreamingPullResponse{ReceivedMessages: []*pb.ReceivedMessage{{AckId: "receipt", Message: &pb.PubsubMessage{MessageId: "event", Data: []byte(`{}`), Attributes: map[string]string{"eventType": "OBJECT_FINALIZE", "bucketId": "b", "objectId": "app.json"}}}}}); err != nil {
		return err
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		if len(req.AckIds) > 0 {
			b.settled <- true
		}
		for _, seconds := range req.ModifyDeadlineSeconds {
			if seconds == 0 {
				b.settled <- false
			}
		}
	}
}

func TestRealSDKRefreshBeforeAckAndNackOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer()
		peer := &broker{settled: make(chan bool, 8)}
		pb.RegisterSubscriberServer(server, peer)
		go func() { _ = server.Serve(listener) }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		client, err := sdk.NewClient(ctx, "p", option.WithEndpoint(listener.Addr().String()), option.WithoutAuthentication(), option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
		if err != nil {
			cancel()
			server.Stop()
			t.Fatal(err)
		}
		sub := client.Subscriber("s")
		sub.ReceiveSettings.NumGoroutines = 1
		sub.ReceiveSettings.MaxOutstandingMessages = 1
		c, _ := New(Config{Source: GCS, Bucket: "b", Key: "app.json", OnError: func(error) {}}, sub)
		started, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- c.Run(ctx, func(work context.Context) error {
				close(started)
				select {
				case <-finish:
				case <-work.Done():
					return work.Err()
				}
				if fail {
					return errors.New("temporary")
				}
				return nil
			})
		}()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("no delivery")
		}
		select {
		case <-peer.settled:
			t.Fatal("settled before refresh")
		case <-time.After(100 * time.Millisecond):
		}
		close(finish)
		select {
		case ack := <-peer.settled:
			if ack == fail {
				t.Fatalf("ack=%t, failure=%t", ack, fail)
			}
		case <-ctx.Done():
			t.Fatal("no settlement")
		}
		cancel()
		<-done
		if err := client.Close(); err != nil {
			t.Error(err)
		}
		server.Stop()
	}
}
