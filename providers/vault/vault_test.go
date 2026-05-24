//go:build !no_provider_vault

package vault

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

func dataResponse(version int, data map[string]any) []byte {
	body := map[string]any{
		"data": map[string]any{
			"data":     data,
			"metadata": map[string]any{"version": version},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

func metadataResponse(version int) []byte {
	body := map[string]any{
		"data": map[string]any{"current_version": version},
	}
	b, _ := json.Marshal(body)
	return b
}

func TestProvider_LoadExpandsKeys(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Header.Get("X-Vault-Token") != "tok" {
			t.Fatalf("missing token header")
		}
		if _, err := w.Write(dataResponse(3, map[string]any{
			"database.dsn":  "postgres://x",
			"database.pool": "10",
			"flat":          "ok",
		})); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	p, err := New(srv.URL, "myapp/cfg", "tok")
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	db, _ := got["database"].(map[string]any)
	if db == nil || db["dsn"] != "postgres://x" || db["pool"] != "10" {
		t.Fatalf("expand failed: %+v", got)
	}
	if got["flat"] != "ok" {
		t.Fatalf("flat key lost: %+v", got)
	}
}

func TestProvider_WatchOnVersionBump(t *testing.T) {
	var version atomic.Int64
	version.Store(1)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		v := int(version.Load())
		switch {
		case r.URL.Path == "/v1/secret/data/cfg":
			if _, err := w.Write(dataResponse(v, map[string]any{"k": "v"})); err != nil {
				t.Error(err)
			}
		case r.URL.Path == "/v1/secret/metadata/cfg":
			if _, err := w.Write(metadataResponse(v)); err != nil {
				t.Error(err)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	p, _ := New(srv.URL, "cfg", "tok", WithInterval(20*time.Millisecond))
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	ch, _ := p.Watch(ctx)
	// Bump the version after watch starts.
	go func() { time.Sleep(60 * time.Millisecond); version.Store(2) }()
	select {
	case ev := <-ch:
		if ev.Reason != "vault-version" {
			t.Fatalf("bad reason %s", ev.Reason)
		}
	case <-ctx.Done():
		t.Fatalf("no event")
	}
}

func TestProvider_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(403)
		if _, err := w.Write([]byte(`{"errors":["denied"]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	p, _ := New(srv.URL, "x", "tok")
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestNew_Validation(t *testing.T) {
	for _, c := range []struct{ a, p, t string }{{"", "p", "t"}, {"x", "", "t"}, {"x", "p", ""}} {
		if _, err := New(c.a, c.p, c.t); err == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}

func TestProvider_WatchDisabled(t *testing.T) {
	p, _ := New("http://x", "p", "t", WithInterval(0))
	ch, err := p.Watch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ch != nil {
		t.Fatalf("want nil channel when interval=0")
	}
}

// TestProvider_TokenRaceDuringRotation guards against a regression of
// the data race where readData / readMetadataVersion read p.token
// without the lock that renewLoop / ensureToken hold while writing.
// Run with `go test -race`; without loadToken's mutex, the race
// detector flags this reliably.
func TestProvider_TokenRaceDuringRotation(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write(dataResponse(1, map[string]any{"k": "v"}))
	}))
	defer srv.Close()

	p, err := New(srv.URL, "cfg", "initial-token")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	// Readers: hammer Load (which calls loadToken under the lock).
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_, _ = p.Load(ctx)
			}
		}()
	}
	// Writer: simulate the auth renewer rotating the token under p.mu.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ctx.Err() == nil; n++ {
			p.mu.Lock()
			p.token = fmt.Sprintf("rotated-%d", n)
			p.mu.Unlock()
		}
	}()
	wg.Wait()
}

// TestProvider_WatchLoopRetriesAfterBackpressure guards B2: a dropped
// event must NOT permanently advance p.version, otherwise the next tick
// sees v==old and the config update is silently lost until the remote
// bumps the version again.
func TestProvider_WatchLoopRetriesAfterBackpressure(t *testing.T) {
	var version atomic.Int64
	version.Store(1)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		v := int(version.Load())
		switch {
		case r.URL.Path == "/v1/secret/data/cfg":
			_, _ = w.Write(dataResponse(v, map[string]any{"k": "v"}))
		case r.URL.Path == "/v1/secret/metadata/cfg":
			_, _ = w.Write(metadataResponse(v))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	p, _ := New(srv.URL, "cfg", "tok", WithInterval(10*time.Millisecond))
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Pre-fill the event channel so the first watchLoop send falls into
	// the non-blocking-default drop branch.
	out := make(chan contracts.Event, 1)
	out <- contracts.Event{Source: "pre-fill"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.watchLoop(ctx, out)

	// Bump remote version while the channel is still full — watchLoop
	// should detect the change but drop the send and leave p.version
	// alone.
	version.Store(2)

	// Wait long enough for several ticks (each 10ms) to hit the drop
	// path.
	time.Sleep(100 * time.Millisecond)

	// Drain the pre-fill so the channel has room.
	<-out

	// Now a subsequent tick must re-emit because p.version was never
	// advanced past 1.
	select {
	case ev := <-out:
		if ev.Reason != "vault-version" {
			t.Fatalf("bad reason %q", ev.Reason)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("event was never re-emitted after channel drained — version got prematurely committed")
	}
}
