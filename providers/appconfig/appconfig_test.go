package appconfig

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/fastabc/fastconf/contracts"
)

type fakeAPI struct {
	starts int
	tokens []string
	get    func(int) (*sdk.GetLatestConfigurationOutput, error)
}

func (f *fakeAPI) StartConfigurationSession(context.Context, *sdk.StartConfigurationSessionInput, ...func(*sdk.Options)) (*sdk.StartConfigurationSessionOutput, error) {
	f.starts++
	return &sdk.StartConfigurationSessionOutput{InitialConfigurationToken: aws.String("initial")}, nil
}
func (f *fakeAPI) GetLatestConfiguration(_ context.Context, in *sdk.GetLatestConfigurationInput, _ ...func(*sdk.Options)) (*sdk.GetLatestConfigurationOutput, error) {
	f.tokens = append(f.tokens, aws.ToString(in.ConfigurationToken))
	return f.get(len(f.tokens))
}

func response(body, token string) *sdk.GetLatestConfigurationOutput {
	return &sdk.GetLatestConfigurationOutput{Configuration: []byte(body), ContentType: aws.String("application/json"), NextPollConfigurationToken: aws.String(token), NextPollIntervalInSeconds: 60}
}

func TestSessionTokensCacheAndRejectedDocument(t *testing.T) {
	bodies := []string{`{"nested":{"value":"one"}}`, "", `secret-invalid-json`, "", `{"nested":{"value":"two"}}`}
	f := &fakeAPI{get: func(n int) (*sdk.GetLatestConfigurationOutput, error) {
		return response(bodies[n-1], string(rune('a'+n-1))), nil
	}}
	p, err := New(Config{Application: "a", Environment: "e", Profile: "p"}, f)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.now = func() time.Time { return now }
	first, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.Map["nested"].(map[string]any)["value"] = "mutated"
	cached, err := p.Load(context.Background())
	if err != nil || cached.Map["nested"].(map[string]any)["value"] != "one" || len(f.tokens) != 1 {
		t.Fatalf("cache = %+v, %v", cached, err)
	}
	for i := 1; i < len(bodies); i++ {
		now = now.Add(time.Minute)
		snap, err := p.Load(context.Background())
		if i == 2 || i == 3 {
			if err == nil || strings.Contains(err.Error(), "secret-invalid") || snap.Map != nil {
				t.Fatalf("rejected document concealed/leaked: %+v, %v", snap, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && snap.Revision != first.Revision {
			t.Fatal("empty response changed revision")
		}
		if i == 4 && snap.Map["nested"].(map[string]any)["value"] != "two" {
			t.Fatal("did not recover")
		}
	}
	if f.starts != 1 || strings.Join(f.tokens, ",") != "initial,a,b,c,d" {
		t.Fatalf("tokens: %v, starts=%d", f.tokens, f.starts)
	}
}

func TestSessionRecreatedAfterUncertainRequest(t *testing.T) {
	failure := errors.New("transport token=secret")
	f := &fakeAPI{get: func(n int) (*sdk.GetLatestConfigurationOutput, error) {
		if n == 1 {
			return nil, failure
		}
		return response(`{"ok":true}`, "next"), nil
	}}
	p, _ := New(Config{Application: "a", Environment: "e", Profile: "p"}, f)
	now := time.Now()
	p.now = func() time.Time { return now }
	_, err := p.Load(context.Background())
	if !errors.Is(err, failure) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("failure = %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := p.Load(context.Background()); err != nil || f.starts != 2 {
		t.Fatalf("recovery = %v; starts=%d", err, f.starts)
	}
}

func TestLimitsCancellationAndValidation(t *testing.T) {
	f := &fakeAPI{get: func(int) (*sdk.GetLatestConfigurationOutput, error) { return response(`{"ok":true}`, "next"), nil }}
	cfg := Config{Application: "a", Environment: "e", Profile: "p"}
	p, _ := New(cfg, f, WithMaxBodyBytes(2))
	if _, err := p.Load(context.Background()); !errors.Is(err, contracts.ErrTooLarge) {
		t.Fatalf("limit = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, invalid := range []Config{{}, {Application: "a", Environment: "e", Profile: "p", MinimumPollInterval: time.Second}, {Application: "a", Environment: "e", Profile: "p", Codec: "unknown"}} {
		if _, err := New(invalid, f); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("accepted nil client")
	}
}

var _ API = (*sdk.Client)(nil)

type blockingAPI struct{ entered chan struct{} }

func (f *blockingAPI) StartConfigurationSession(ctx context.Context, _ *sdk.StartConfigurationSessionInput, _ ...func(*sdk.Options)) (*sdk.StartConfigurationSessionOutput, error) {
	close(f.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*blockingAPI) GetLatestConfiguration(context.Context, *sdk.GetLatestConfigurationInput, ...func(*sdk.Options)) (*sdk.GetLatestConfigurationOutput, error) {
	panic("canceled session must not fetch a document")
}

func TestLoadWaitHonorsCancellation(t *testing.T) {
	client := &blockingAPI{entered: make(chan struct{})}
	p, err := New(Config{Application: "a", Environment: "e", Profile: "p"}, client)
	if err != nil {
		t.Fatal(err)
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := p.Load(firstCtx); first <- err }()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("first load did not start")
	}

	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second := make(chan error, 1)
	go func() { _, err := p.Load(waiting); second <- err }()
	select {
	case err := <-second:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("load ignored cancellation while waiting")
	}

	cancelFirst()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("active load ignored cancellation")
	}
}
