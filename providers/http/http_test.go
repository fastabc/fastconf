package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/internal/testutil"

	"github.com/fastabc/fastconf/contracts"
	httpprov "github.com/fastabc/fastconf/providers/http"
)

type yamlCodec struct{}

// Decode is a no-frills YAML codec for tests; we re-use the framework's
// public yaml codec by importing through a tiny adapter to avoid pulling
// gopkg.in/yaml.v3 into this package's test surface.
func (yamlCodec) Decode(data []byte) (map[string]any, error) {
	// Tiny "key: value" line parser is enough for these tests.
	out := map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			return nil, errors.New("bad line: " + line)
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	return out, nil
}

func TestNew_Validation(t *testing.T) {
	if _, err := httpprov.New("", "http://x", yamlCodec{}); err == nil {
		t.Error("expected error on empty name")
	}
	if _, err := httpprov.New("n", "", yamlCodec{}); err == nil {
		t.Error("expected error on empty url")
	}
	if _, err := httpprov.New("n", "http://x", nil); err != nil {
		t.Error("nil codec should select by Content-Type:", err)
	}
}

func TestProvider_LoadAndETagShortCircuit(t *testing.T) {
	calls := atomic.Int32{}
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		calls.Add(1)
		const etag = `"v1"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(nethttp.StatusNotModified)
			w.Header().Set("ETag", etag)
			return
		}
		w.Header().Set("ETag", etag)
		_, _ = io.WriteString(w, "key: hello")
	}))
	defer srv.Close()

	p, err := httpprov.New("test", srv.URL, yamlCodec{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out["key"] != "hello" {
		t.Errorf("decoded: %v", out)
	}
	// Second load should re-use cached body when server returns 304.
	out2, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatalf("Load #2: %v", err)
	}
	if out2["key"] != "hello" {
		t.Errorf("304 fallthrough lost data: %v", out2)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 server hits, got %d", got)
	}
}

func TestProvider_WatchEmitsOnDiff(t *testing.T) {
	current := atomic.Pointer[string]{}
	v1 := "key: a"
	current.Store(&v1)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = io.WriteString(w, *current.Load())
	}))
	defer srv.Close()

	p, err := httpprov.New("watch", srv.URL, yamlCodec{}, httpprov.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := p.Watch(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the response; expect at least one event.
	go func() {
		time.Sleep(80 * time.Millisecond)
		v2 := "key: b"
		current.Store(&v2)
	}()
	select {
	case ev := <-ch:
		if ev.Source != "watch" {
			t.Errorf("source: %v", ev.Source)
		}
	case <-ctx.Done():
		t.Fatal("no event received")
	}

	// Implements the contracts.Provider interface.
	var _ contracts.Provider = p
}

func TestProvider_RejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = io.WriteString(w, "key: too-large")
	}))
	defer srv.Close()
	p, err := httpprov.New("limited", srv.URL, yamlCodec{}, httpprov.WithMaxBodyBytes(4))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); !errors.Is(err, contracts.ErrConfigTooLarge) {
		t.Fatalf("Load error = %v, want ErrConfigTooLarge", err)
	}
}

func TestContentTypeAndManualReload(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		hits++
		if r.Header.Get("If-None-Match") == "rev1" {
			w.WriteHeader(nethttp.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", "rev1")
		_, _ = w.Write([]byte(`{"port":8080}`))
	}))
	defer srv.Close()
	p, err := httpprov.New("remote", srv.URL, nil, httpprov.WithInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		snap, err := p.Load(context.Background())
		if err != nil || snap.Map["port"] != json.Number("8080") || snap.Revision != "rev1" {
			t.Fatalf("Load = %#v, %v", snap, err)
		}
	}
	ch, err := p.Watch(context.Background(), "")
	if err != nil || ch != nil || hits != 2 {
		t.Fatalf("Watch = %v, %v; hits=%d", ch, err, hits)
	}
}
