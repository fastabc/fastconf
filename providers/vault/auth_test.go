package vault

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/internal/testutil"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/contracts/providertest"
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

	m, err := testutil.Map(p.Load(ctx))
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
	if _, err := p.Watch(ctx, ""); err != nil {
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

func TestWithRenewBeforeZeroDisablesSession(t *testing.T) {
	var calls atomic.Int32
	p, err := New("http://vault.invalid", "app", "", WithAuth(AuthFunc(func(context.Context) (string, time.Duration, error) {
		calls.Add(1)
		return "static", time.Second, nil
	})), WithRenewBefore(0), WithInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ensureToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	ch, err := p.Watch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if ch != nil {
		t.Fatal("renewBefore=0 should disable an interval=0 watch session")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("login calls=%d want 1", got)
	}
}

func TestWatchSessionsCloseAndRenewAgain(t *testing.T) {
	var calls atomic.Int32
	p, err := New("http://vault.invalid", "app", "", WithAuth(AuthFunc(func(context.Context) (string, time.Duration, error) {
		calls.Add(1)
		return "token", 6 * time.Second, nil
	})), WithRenewBefore(5*time.Second), WithInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	for session := 0; session < 2; session++ {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		ch, err := p.Watch(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case event := <-ch:
			if event.Reason != "lease-renew" {
				t.Fatalf("session %d: event = %+v", session, event)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("session %d did not renew", session)
		}
		cancel()
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatal("unexpected event after renewal")
			}
		case <-time.After(time.Second):
			t.Fatalf("session %d channel did not close", session)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("login calls = %d, want initial login and two renewals", got)
	}
}

func TestWatchPollingClosesOnCancel(t *testing.T) {
	p, err := New("http://vault.invalid", "app", "static", WithInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	providertest.AssertWatchClosesOnCancel(t, p, time.Second)
}

func TestRenewFailurePreservesTTLAndRetries(t *testing.T) {
	var calls atomic.Int32
	p, err := New("http://vault.invalid", "app", "token", WithAuth(AuthFunc(func(context.Context) (string, time.Duration, error) {
		if calls.Add(1) == 1 {
			return "", 0, errors.New("temporary login failure")
		}
		return "replacement", 20 * time.Second, nil
	})), WithRenewBefore(5*time.Second), WithInterval(0))
	if err != nil {
		t.Fatal(err)
	}
	p.tokenTTL.Store(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan contracts.Event, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.renewLoop(ctx, out)
	}()
	defer func() { cancel(); <-done }()
	deadline := time.After(3 * time.Second)
	for calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("renewal did not run")
		case <-time.After(time.Millisecond):
		}
	}
	// A failed renewal must not turn the issued two-second TTL into retry state.
	if got := p.tokenTTL.Load(); got != 2 {
		t.Fatalf("failed renewal changed TTL to %d", got)
	}
	select {
	case <-out:
		if got := p.tokenTTL.Load(); got != 20 || p.loadToken() != "replacement" {
			t.Fatalf("successful retry did not update token and TTL: %d", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("renewal did not retry")
	}
}
