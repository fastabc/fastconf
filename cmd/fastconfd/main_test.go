package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
		fastconf.WithObserver(bus),
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
	s.token = "the-correct-secret"
	mux := s.routes()
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"short", "x", http.StatusUnauthorized},
		{"long", "the-correct-secret-extra", http.StatusUnauthorized},
		{"last byte differs", "the-correct-secrey", http.StatusUnauthorized},
		{"all bytes differ", strings.Repeat("x", len(s.token)), http.StatusUnauthorized},
		{"valid", s.token, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/reload", nil)
			req.Header.Set("X-Reload-Token", tc.token)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body)
			}
		})
	}
}

func TestServer_ReadAuth(t *testing.T) {
	s, done := newTestServer(t)
	defer done()
	s.readToken = "read-secret"
	mux := s.routes()
	for _, path := range []string{"/config", "/dump", "/events"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: got %d", path, rr.Code)
		}
		if path == "/events" {
			continue
		}
		req.Header.Set("X-Config-Token", "read-secret")
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s with token: got %d", path, rr.Code)
		}
	}
}

func TestAuthGateRequiresBothTokens(t *testing.T) {
	for _, tc := range []struct {
		name      string
		token     string
		readToken string
		wantErr   bool
	}{
		{"both present", "r", "c", false},
		{"reload token missing", "", "c", true},
		{"read token missing", "r", "", true},
		{"both missing", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := authGate(tc.token, tc.readToken)
			if (err != nil) != tc.wantErr {
				t.Fatalf("authGate(%q, %q) = %v, wantErr %v", tc.token, tc.readToken, err, tc.wantErr)
			}
		})
	}
}

func TestServer_EventsSSE(t *testing.T) {
	s, done := newTestServer(t)
	defer done()
	s.readToken = "read-secret"
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Config-Token", s.readToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE headers must arrive before the first commit: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type: %q", got)
	}
	if err := s.mgr.Reload(ctx, fastconf.WithOverride(map[string]any{"a": 2})); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() || scanner.Text() != "event: reload" {
		t.Fatalf("event line = %q, err = %v", scanner.Text(), scanner.Err())
	}
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "data: ") {
		t.Fatalf("data line = %q, err = %v", scanner.Text(), scanner.Err())
	}
	var cause fastconf.ReloadCause
	if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &cause); err != nil {
		t.Fatal(err)
	}
	if cause.Reason != "override" || cause.At != s.mgr.Snapshot().Cause().At {
		t.Fatalf("unexpected commit cause: %+v", cause)
	}
	if !scanner.Scan() || scanner.Text() != "" {
		t.Fatalf("missing SSE frame terminator: %q, err = %v", scanner.Text(), scanner.Err())
	}
}

type failingSSEWriter struct {
	header  http.Header
	writes  int
	flushes int
}

func (w *failingSSEWriter) Header() http.Header { return w.header }
func (w *failingSSEWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}
func (w *failingSSEWriter) WriteHeader(int) {}
func (w *failingSSEWriter) Flush()          { w.flushes++ }

func TestServer_EventsStopsWhenClientWriteFails(t *testing.T) {
	s, done := newTestServer(t)
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/events", nil)
	w := &failingSSEWriter{header: make(http.Header)}
	finished := make(chan struct{})
	go func() {
		s.handleEvents(w, req)
		close(finished)
	}()

	ready := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.bus.mu.Lock()
		ready = len(s.bus.subs) == 1
		s.bus.mu.Unlock()
		if ready {
			break
		}
		runtime.Gosched()
	}
	if !ready {
		t.Fatal("SSE handler did not subscribe")
	}
	s.bus.publish(fastconf.ReloadCause{})

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after a client write failure")
	}
	if w.writes != 1 || w.flushes != 1 {
		t.Fatalf("writes=%d flushes=%d, want one failed write and only the initial header flush", w.writes, w.flushes)
	}
}

// TestServer_ConfigRedactsByDefault verifies: /config and /dump mask
// secrets unless the request asks for ?unredacted=true and carries the
// separate unredacted token.
func TestServer_ConfigRedactsByDefault(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("db:\n  host: h\n  password: hunter2\n")},
	}
	mgr, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithSecretPaths(defaultSecretPaths...))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	s := newServer(mgr, newEventBus(), "", nil)
	s.unredactedToken = "plain-secret"
	mux := s.routes()
	get := func(target, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if token != "" {
			req.Header.Set("X-Unredacted-Token", token)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	for _, target := range []string{"/config", "/config?path=db", "/dump", "/dump?format=json"} {
		t.Run(target, func(t *testing.T) {
			for _, token := range []string{"", "plain-secret"} {
				rr := get(target, token)
				if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "hunter2") || !strings.Contains(rr.Body.String(), "REDACTED") {
					t.Fatalf("default: %d %s; want 200 with masked secret", rr.Code, rr.Body)
				}
			}
			sep := "?"
			if strings.Contains(target, "?") {
				sep = "&"
			}
			plainTarget := target + sep + "unredacted=true"
			for _, token := range []string{"", "wrong"} {
				if rr := get(plainTarget, token); rr.Code != http.StatusUnauthorized {
					t.Fatalf("unredacted with token %q: %d; want 401", token, rr.Code)
				}
			}
			if rr := get(plainTarget, "plain-secret"); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "hunter2") {
				t.Fatalf("unredacted with token: %d %s; want plaintext", rr.Code, rr.Body)
			}
		})
	}
}
