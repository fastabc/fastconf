//go:build !no_provider_vault

package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppRoleAuth_LoginAndRenew(t *testing.T) {
	var loginCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		n := loginCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"auth": map[string]any{
				"client_token":   "tok-" + string(rune('0'+n)),
				"lease_duration": 6, // seconds; renewer will fire ~5s before
			},
		})
	})
	mux.HandleFunc("/v1/secret/data/myapp", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Token"); got == "" {
			t.Errorf("missing token header")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data":     map[string]any{"k": "v"},
				"metadata": map[string]any{"version": 1},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	auth := AppRoleAuth(srv.URL, "rid", "sid", srv.Client())
	p, err := New(srv.URL, "myapp", "", WithAuth(auth), WithRenewBefore(5*time.Second), WithInterval(0), WithClient(srv.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, err := p.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m["k"] != "v" {
		t.Fatalf("unexpected payload %+v", m)
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("login not invoked")
	}

	// Trigger watch (registers renewer).
	if _, err := p.Watch(ctx); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	// Wait for renewer to fire (lease 6s, renewBefore 5s -> wait ~1s, but
	// minimum wait is 1s).
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if loginCalls.Load() >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if loginCalls.Load() < 2 {
		t.Fatalf("renewer never re-logged in (calls=%d)", loginCalls.Load())
	}
}

func TestTokenAuth_NoRenew(t *testing.T) {
	a := TokenAuth("static")
	tok, ttl, err := a.Login(context.Background())
	if err != nil || tok != "static" || ttl != 0 {
		t.Fatalf("got %q %v %v", tok, ttl, err)
	}
}
