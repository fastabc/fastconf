//go:build !no_provider_vault

package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// Auth abstracts how a Vault token is acquired (and re-acquired). Real
// deployments rarely use static tokens — they bootstrap via AppRole,
// Kubernetes ServiceAccount, AWS IAM, etc. — and the resulting token
// has a TTL that must be renewed before it expires. Implementations
// MUST be goroutine-safe and SHOULD return a token whose TTL is
// honoured by the calling Vault instance.
type Auth interface {
	// Login obtains a fresh token. ttl reflects the server-issued TTL;
	// a zero ttl means "non-expiring" (the renewer will skip).
	Login(ctx context.Context) (token string, ttl time.Duration, err error)
}

// AuthFunc adapts a free function to Auth.
type AuthFunc func(context.Context) (string, time.Duration, error)

// Login implements Auth.
func (f AuthFunc) Login(ctx context.Context) (string, time.Duration, error) { return f(ctx) }

// TokenAuth is a degenerate Auth that always returns the same static
// token and a zero TTL. It exists for parity with the legacy New()
// signature and for non-renewing service tokens.
func TokenAuth(token string) Auth {
	return AuthFunc(func(_ context.Context) (string, time.Duration, error) {
		return token, 0, nil
	})
}

// AppRoleAuth performs a POST against /v1/auth/approle/login with
// role_id and secret_id, returning the issued client token + lease
// TTL. The Doer is reused across renewals.
//
// When client is nil, AppRoleAuth uses an isolated *http.Client with a
// 10s timeout — never net/http.DefaultClient. Sharing the default client
// across managers can leak idle connections after Close and entangle
// timeouts across processes.
func AppRoleAuth(addr, roleID, secretID string, client Doer) Auth {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	return AuthFunc(func(ctx context.Context) (string, time.Duration, error) {
		body := map[string]string{"role_id": roleID, "secret_id": secretID}
		buf, _ := json.Marshal(body)
		url := fmt.Sprintf("%s/v1/auth/approle/login", addr)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, bytes.NewReader(buf))
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode != nethttp.StatusOK {
			return "", 0, fmt.Errorf("approle: status %d: %s", resp.StatusCode, string(raw))
		}
		var out struct {
			Auth struct {
				ClientToken   string `json:"client_token"`
				LeaseDuration int    `json:"lease_duration"`
			} `json:"auth"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", 0, fmt.Errorf("approle: decode: %w", err)
		}
		if out.Auth.ClientToken == "" {
			return "", 0, errors.New("approle: empty client_token")
		}
		return out.Auth.ClientToken, time.Duration(out.Auth.LeaseDuration) * time.Second, nil
	})
}

// WithAuth wires an Auth provider in place of the static token passed
// to New. The Provider will Login() once at first use and again when
// the renewer (see WithRenewBefore) decides the lease is too close to
// expiry.
func WithAuth(a Auth) Option { return func(p *Provider) { p.auth = a } }

// WithRenewBefore controls how far ahead of TTL the lease renewer
// re-Logins (default 30s, never less than 5s). Set to 0 to disable
// renewal entirely (useful for static tokens).
func WithRenewBefore(d time.Duration) Option {
	return func(p *Provider) { p.renewBefore = d }
}

// startAuthRenewer kicks off the lease-renewal goroutine. Safe to call
// multiple times — the goroutine is started lazily via sync.Once.
func (p *Provider) startAuthRenewer(ctx context.Context, out chan<- contracts.Event) {
	p.renewOnce.Do(func() {
		go p.renewLoop(ctx, out)
	})
}

func (p *Provider) renewLoop(ctx context.Context, out chan<- contracts.Event) {
	for {
		ttl := p.tokenTTL.Load()
		if ttl <= 0 {
			return // non-expiring token; nothing to do
		}
		before := p.renewBefore
		if before <= 0 {
			before = 30 * time.Second
		}
		if before < 5*time.Second {
			before = 5 * time.Second
		}
		wait := time.Duration(ttl)*time.Second - before
		if wait < time.Second {
			wait = time.Second
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		token, newTTL, err := p.auth.Login(ctx)
		if err != nil {
			// Retry interval = min(currentTTL/2, 15s) so short-lived dynamic
			// secrets don't blow past expiry while we wait for the next
			// auth.Login round.
			retry := int64(15)
			if half := ttl / 2; half > 0 && half < retry {
				retry = half
			}
			if retry < 1 {
				retry = 1
			}
			p.tokenTTL.Store(retry)
			continue
		}
		p.mu.Lock()
		p.token = token
		p.mu.Unlock()
		p.tokenTTL.Store(int64(newTTL / time.Second))
		select {
		case out <- contracts.Event{Source: p.name, Reason: "lease-renew", At: time.Now()}:
		case <-ctx.Done():
			return
		default:
		}
	}
}

// ensureToken is invoked by Load/Watch lazily to perform the first
// Login when WithAuth was used.
func (p *Provider) ensureToken(ctx context.Context) error {
	if p.auth == nil {
		return nil
	}
	p.mu.Lock()
	if p.token != "" {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	tok, ttl, err := p.auth.Login(ctx)
	if err != nil {
		return fmt.Errorf("vault: auth login: %w", err)
	}
	p.mu.Lock()
	p.token = tok
	p.mu.Unlock()
	p.tokenTTL.Store(int64(ttl / time.Second))
	return nil
}

// (Auth fields are stored on Provider directly.)
