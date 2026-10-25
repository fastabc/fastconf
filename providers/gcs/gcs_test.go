package gcs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"cloud.google.com/go/storage"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/gcs"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type store struct {
	mu             sync.Mutex
	body           string
	generation     int64
	metadataStatus int
	readStatus     int
	reads          int
	pinned         string
}

func (s *store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.Contains(r.URL.Path, "/b/configs/o/app.json") {
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("alt") != "media" {
		if s.metadataStatus != 0 {
			w.WriteHeader(s.metadataStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"bucket": "configs", "name": "app.json", "generation": strconv.FormatInt(s.generation, 10), "size": strconv.Itoa(len(s.body)),
		})
		return
	}
	s.reads++
	s.pinned = r.URL.Query().Get("generation")
	if s.readStatus != 0 {
		w.WriteHeader(s.readStatus)
		return
	}
	w.Header().Set("X-Goog-Generation", strconv.FormatInt(s.generation, 10))
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, s.body)
}

func clientFor(t *testing.T, s http.Handler) *storage.Client {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	client, err := storage.NewClient(context.Background(), option.WithEndpoint(srv.URL+"/storage/v1/"), option.WithoutAuthentication(), option.WithHTTPClient(srv.Client()), storage.WithJSONReads(), storage.WithDisabledClientMetrics())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func provider(t *testing.T, s *store, opts ...gcs.Option) *gcs.Provider {
	t.Helper()
	p, err := gcs.New(gcs.Config{Bucket: "configs", Key: "app.json"}, clientFor(t, s), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGenerationCacheAndRefresh(t *testing.T) {
	s := &store{body: `{"nested":{"key":"v1"}}`, generation: 1}
	p := provider(t, s)
	first, err := p.Load(context.Background())
	if err != nil || first.Revision != "1" {
		t.Fatalf("Load = %+v, %v", first, err)
	}
	first.Map["nested"].(map[string]any)["key"] = "mutated"
	s.mu.Lock()
	s.body = "invalid"
	s.mu.Unlock()
	second, err := p.Load(context.Background())
	s.mu.Lock()
	reads, pinned := s.reads, s.pinned
	s.mu.Unlock()
	if err != nil || second.Map["nested"].(map[string]any)["key"] != "v1" || second.Revision != "1" || reads != 1 || pinned != "1" {
		t.Fatalf("cached Load = %+v, %v; reads=%d, generation=%q", second, err, reads, pinned)
	}
	s.mu.Lock()
	s.generation, s.body = 2, `{"nested":{"key":"v2"}}`
	s.mu.Unlock()
	third, err := p.Load(context.Background())
	if err != nil || third.Revision != "2" || third.Map["nested"].(map[string]any)["key"] != "v2" {
		t.Fatalf("refresh = %+v, %v", third, err)
	}
	if p.Name() != "gcs://configs/app.json" || p.Describe().Priority != contracts.PriorityKV {
		t.Fatalf("metadata = %s %+v", p.Name(), p.Describe())
	}
}

func TestRejectedDocumentsDoNotSeedCache(t *testing.T) {
	for _, body := range []string{`invalid`, `{"key":"oversized"}`} {
		t.Run(body, func(t *testing.T) {
			s := &store{body: body, generation: 1}
			p := provider(t, s, gcs.WithMaxBodyBytes(8))
			if _, err := p.Load(context.Background()); err == nil {
				t.Fatal("expected rejection")
			} else if len(body) > 8 && !errors.Is(err, contracts.ErrTooLarge) {
				t.Fatalf("oversized error = %v", err)
			}
			s.mu.Lock()
			s.body = `{"ok":1}`
			s.mu.Unlock()
			snap, err := p.Load(context.Background())
			if err != nil || snap.Map["ok"] != json.Number("1") {
				t.Fatalf("recovery = %+v, %v", snap, err)
			}
		})
	}
}

func TestLoadErrorsAndCancellation(t *testing.T) {
	for _, s := range []*store{{metadataStatus: 404}, {generation: 1, readStatus: 404}} {
		p := provider(t, s)
		if _, err := p.Load(context.Background()); !errors.Is(err, storage.ErrObjectNotExist) {
			t.Fatalf("missing object = %v", err)
		}
	}
	p := provider(t, &store{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Load = %v", err)
	}
}

func TestNewValidationAndCodec(t *testing.T) {
	client := clientFor(t, &store{})
	for _, cfg := range []gcs.Config{{}, {Bucket: "b"}, {Key: "a.json"}, {Bucket: "b", Key: "a.unknown"}} {
		if _, err := gcs.New(cfg, client); err == nil {
			t.Fatalf("accepted config %+v", cfg)
		}
	}
	if _, err := gcs.New(gcs.Config{Bucket: "b", Key: "a.json"}, nil); err == nil {
		t.Fatal("accepted nil client")
	}
	p, err := gcs.New(gcs.Config{Bucket: "b", Key: "no-extension", Codec: "yaml", Priority: 99}, client)
	if err != nil || p.Describe().Priority != 99 {
		t.Fatalf("explicit codec = %v, %v", p, err)
	}
	if _, err := gcs.New(gcs.Config{Bucket: "b", Key: "no-extension"}, client); !errors.Is(err, codec.ErrUnknownCodec) {
		t.Fatalf("unknown codec = %v", err)
	}
}

func TestManagerIntegration(t *testing.T) {
	p := provider(t, &store{body: `{"host":"production"}`, generation: 1}, gcs.WithMaxBodyBytes(-1))
	type config struct {
		Host string `json:"host"`
	}
	snap, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
	if err != nil || snap.Value().Host != "production" {
		t.Fatalf("Load = %+v, %v", snap, err)
	}
}

func TestDecodeErrorHidesDocument(t *testing.T) {
	s := &store{body: "password: !!int FAKESECRET\n", generation: 1}
	p, err := gcs.New(gcs.Config{Bucket: "configs", Key: "app.json", Codec: "yaml"}, clientFor(t, s))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Load(context.Background())
	if err == nil || strings.Contains(err.Error(), "FAKESECRET") {
		t.Fatalf("unsafe error: %v", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("lost decoder error")
	}
}

func TestSDKErrorHidesResponseDetails(t *testing.T) {
	client := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":403,"message":"FAKESECRET"}}`)
	}))
	p, err := gcs.New(gcs.Config{Bucket: "configs", Key: "app.json"}, client)
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Load(context.Background())
	if err == nil || strings.Contains(err.Error(), "FAKESECRET") {
		t.Fatalf("unsafe error: %v", err)
	}
	var cause *googleapi.Error
	if !errors.As(err, &cause) || cause.Code != http.StatusForbidden {
		t.Fatalf("lost SDK error classification: %v", err)
	}
}

func TestLoadWaitHonorsCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var once sync.Once
	s := &store{body: `{ "ok": true }`, generation: 1}
	client := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			s.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	}))
	p, err := gcs.New(gcs.Config{Bucket: "configs", Key: "app.json"}, client)
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
