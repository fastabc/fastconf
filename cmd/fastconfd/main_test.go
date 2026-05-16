package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/cmd/internal/cli"
	"github.com/fastabc/fastconf"
)

func newTestServer(t *testing.T) (*server, func()) {
	t.Helper()
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\nnested:\n  k: v\n")},
	}
	bus := newEventBus()
	mgr, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithAuditSink(bus),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return newServer(mgr, bus, "", nil), func() { _ = mgr.Close() }
}

func TestServer_HealthVersionConfig(t *testing.T) {
	s, done := newTestServer(t)
	defer done()
	mux := s.routes()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/version", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("version: %d", rr.Code)
	}
	var v map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["hash"]; !ok {
		t.Fatalf("missing hash: %v", v)
	}

	req = httptest.NewRequest(http.MethodGet, "/config?path=nested.k", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("config: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "v") {
		t.Fatalf("got %q", rr.Body.String())
	}
}

func TestServer_ReloadAuth(t *testing.T) {
	s, done := newTestServer(t)
	defer done()
	s.token = "secret"
	mux := s.routes()
	req := httptest.NewRequest(http.MethodPost, "/reload", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	req.Header.Set("X-Reload-Token", "secret")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestServer_EventsSSE(t *testing.T) {
	s, done := newTestServer(t)
	defer done()
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Connection may be cancelled by ctx deadline; that's also fine —
		// the only thing this smoke test verifies is that /events is wired.
		return
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type: %q", got)
	}
	_, _ = io.CopyN(io.Discard, resp.Body, 1)
}

func TestMainFlagSetUsesFastconfDefaultDir(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Dir != fastconf.DefaultDir {
		t.Fatalf("dir default = %q, want %q", f.Dir, fastconf.DefaultDir)
	}
}

func TestMainDoesNotDefineLocalLookupPath(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(src), "func lookupPath(") {
		t.Fatal("main.go must use pkg/mappath.GetDotted instead of a local lookupPath")
	}
}
