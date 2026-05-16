//go:build !no_provider_consul

package consul

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type yamlCodec struct{}

func (yamlCodec) Decode(b []byte) (map[string]any, error) {
	// Trivial "k: v" parser only used by the blob test.
	out := map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	return out, nil
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func writePairs(w nethttp.ResponseWriter, idx uint64, pairs []kvPair) {
	w.Header().Set("X-Consul-Index", fmt.Sprintf("%d", idx))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pairs)
}

func TestProvider_LoadKVTree(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		writePairs(w, 5, []kvPair{
			{Key: "myapp/db/host", Value: b64("127.0.0.1"), ModifyIndex: 3},
			{Key: "myapp/db/port", Value: b64("5432"), ModifyIndex: 4},
			{Key: "myapp/cache/", Value: "", ModifyIndex: 5}, // folder
			{Key: "myapp/cache/ttl", Value: b64("60"), ModifyIndex: 5},
		})
	}))
	defer srv.Close()

	p, err := New(srv.URL, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	db, _ := got["db"].(map[string]any)
	if db == nil || db["host"] != "127.0.0.1" || db["port"] != "5432" {
		t.Fatalf("bad db tree: %+v", got)
	}
	cache, _ := got["cache"].(map[string]any)
	if cache == nil || cache["ttl"] != "60" {
		t.Fatalf("bad cache tree: %+v", got)
	}
}

func TestProvider_LoadBlob(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		writePairs(w, 7, []kvPair{
			{Key: "cfg/all", Value: b64("name: alpha\nport: 8080"), ModifyIndex: 7},
		})
	}))
	defer srv.Close()
	p, err := New(srv.URL, "cfg", WithMode(ModeBlob), WithCodec(yamlCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "alpha" || got["port"] != "8080" {
		t.Fatalf("blob decode: %+v", got)
	}
}

func TestProvider_WatchEmitsOnIndexChange(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		n := calls.Add(1)
		switch n {
		case 1: // initial Load
			writePairs(w, 1, []kvPair{{Key: "k/a", Value: b64("1")}})
		case 2: // first Watch poll, sees index=1, returns same -> long-poll style; bump index to drive change
			writePairs(w, 2, []kvPair{{Key: "k/a", Value: b64("2")}})
		default:
			// Block-ish: respond with current index so Watch loops without new event.
			writePairs(w, 2, []kvPair{{Key: "k/a", Value: b64("2")}})
		}
	}))
	defer srv.Close()

	p, err := New(srv.URL, "k", WithWait(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := p.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Source != p.Name() {
			t.Fatalf("event src=%s want %s", ev.Source, p.Name())
		}
	case <-ctx.Done():
		t.Fatalf("no event received")
	}
}

func TestNew_Validation(t *testing.T) {
	if _, err := New("", "x"); err == nil {
		t.Fatal("want addr error")
	}
	if _, err := New("http://x", "p", WithMode(ModeBlob)); err == nil {
		t.Fatal("want missing codec error")
	}
}

func TestProvider_404IsEmpty(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("X-Consul-Index", "9")
		w.WriteHeader(nethttp.StatusNotFound)
	}))
	defer srv.Close()
	p, _ := New(srv.URL, "missing")
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
