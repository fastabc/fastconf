package azblob_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	sdk "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/azblob"
)

func TestNetworkErrorRedactsSAS(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	client, err := sdk.NewClientWithNoCredential(srv.URL+"?sig=review-secret", &sdk.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := azblob.New(azblob.Config{Container: "configs", Key: "app.json"}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Load(context.Background())
	var transport *url.Error
	if !errors.As(err, &transport) {
		t.Fatalf("lost transport error: %v", err)
	}
	if strings.Contains(err.Error(), "review-secret") || strings.Contains(err.Error(), "sig=") {
		t.Fatalf("error leaks SAS: %v", err)
	}
}

// Use the real SDK against HTTP so tests exercise Azure's conditional headers
// and response-error translation as well as provider decoding.
type store struct {
	mu      sync.Mutex
	body    string
	etag    string
	status  int
	gets    int
	lastTag string
}

func (s *store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	s.lastTag = r.Header.Get("If-None-Match")
	if r.URL.Path != "/configs/app.json" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	if s.etag != "" && s.lastTag == s.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", s.etag)
	_, _ = io.WriteString(w, s.body)
}

func setup(t *testing.T, s *store, opts ...azblob.Option) *azblob.Provider {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	client, err := sdk.NewClientWithNoCredential(srv.URL+"?sig=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := azblob.New(azblob.Config{Container: "configs", Key: "app.json"}, client, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Name(), "secret") || strings.Contains(p.Name(), "sig=") {
		t.Fatalf("Name leaks SAS: %s", p.Name())
	}
	return p
}

func TestConditionalCacheAndRefresh(t *testing.T) {
	s := &store{body: `{"nested":{"key":"v1"}}`, etag: `"v1"`}
	p := setup(t, s)
	first, err := p.Load(context.Background())
	if err != nil || first.Revision != `"v1"` {
		t.Fatalf("Load = %+v, %v", first, err)
	}
	first.Map["nested"].(map[string]any)["key"] = "mutated"
	s.mu.Lock()
	s.body = "invalid JSON"
	s.mu.Unlock()
	second, err := p.Load(context.Background())
	s.mu.Lock()
	lastTag := s.lastTag
	s.mu.Unlock()
	if err != nil || second.Map["nested"].(map[string]any)["key"] != "v1" || second.Revision != first.Revision || lastTag != `"v1"` {
		t.Fatalf("cached Load = %+v, %v; tag=%q", second, err, lastTag)
	}
	s.mu.Lock()
	s.etag, s.body = `"v2"`, `{"nested":{"key":"v2"}}`
	s.mu.Unlock()
	third, err := p.Load(context.Background())
	if err != nil || third.Revision != `"v2"` || third.Map["nested"].(map[string]any)["key"] != "v2" {
		t.Fatalf("refresh = %+v, %v", third, err)
	}
	if p.Describe().Priority != contracts.PriorityKV {
		t.Fatal("wrong default priority")
	}
}

func TestRejectedDocumentsDoNotSeedCache(t *testing.T) {
	for _, body := range []string{`invalid`, `{"key":"oversized"}`} {
		t.Run(body, func(t *testing.T) {
			s := &store{body: body, etag: `"same"`}
			p := setup(t, s, azblob.WithMaxBodyBytes(8))
			if _, err := p.Load(context.Background()); err == nil {
				t.Fatal("expected rejection")
			} else if len(body) > 8 && !errors.Is(err, contracts.ErrTooLarge) {
				t.Fatalf("oversized error = %v", err)
			}
			s.mu.Lock()
			s.body = `{"ok":1}`
			s.mu.Unlock()
			snap, err := p.Load(context.Background())
			s.mu.Lock()
			lastTag := s.lastTag
			s.mu.Unlock()
			if err != nil || snap.Map["ok"] != json.Number("1") || lastTag != "" {
				t.Fatalf("recovery = %+v, %v; conditional=%q", snap, err, lastTag)
			}
		})
	}
}

func TestLoadErrorsAndCancellation(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusNotModified} {
		p := setup(t, &store{status: status})
		_, err := p.Load(context.Background())
		var response *azcore.ResponseError
		if !errors.As(err, &response) || response.StatusCode != status {
			t.Fatalf("status %d: %v", status, err)
		}
		if strings.Contains(err.Error(), "sig=") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("status error leaks SAS: %v", err)
		}
	}
	s := &store{}
	p := setup(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Load(ctx); !errors.Is(err, context.Canceled) || s.gets != 0 {
		t.Fatalf("cancellation = %v; gets=%d", err, s.gets)
	}
}

func TestNewValidationAndCodec(t *testing.T) {
	client, _ := sdk.NewClientWithNoCredential("https://account.blob.core.windows.net", nil)
	for _, cfg := range []azblob.Config{{}, {Container: "c"}, {Key: "a.json"}, {Container: "c", Key: "a.unknown"}} {
		if _, err := azblob.New(cfg, client); err == nil {
			t.Fatalf("accepted config %+v", cfg)
		}
	}
	if _, err := azblob.New(azblob.Config{Container: "c", Key: "a.json"}, nil); err == nil {
		t.Fatal("accepted nil client")
	}
	p, err := azblob.New(azblob.Config{Container: "c", Key: "no-extension", Codec: "yaml", Priority: 99}, client)
	if err != nil || p.Describe().Priority != 99 {
		t.Fatalf("explicit codec = %v, %v", p, err)
	}
	if _, err := azblob.New(azblob.Config{Container: "c", Key: "no-extension"}, client); !errors.Is(err, codec.ErrUnknownCodec) {
		t.Fatalf("unknown codec = %v", err)
	}
	var _ azblob.API = (*sdk.Client)(nil)
}

func TestManagerIntegration(t *testing.T) {
	p := setup(t, &store{body: `{"host":"production"}`, etag: `"one"`}, azblob.WithMaxBodyBytes(0))
	type config struct {
		Host string `json:"host"`
	}
	snap, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
	if err != nil || snap.Value().Host != "production" {
		t.Fatalf("Load = %+v, %v", snap, err)
	}
}

func TestLoadWaitHonorsCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			_, _ = io.WriteString(w, `{ "ok": true }`)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	client, err := sdk.NewClientWithNoCredential(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := azblob.New(azblob.Config{Container: "configs", Key: "app.json"}, client)
	if err != nil {
		t.Fatal(err)
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := p.Load(firstCtx); first <- err }()
	select {
	case <-entered:
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
