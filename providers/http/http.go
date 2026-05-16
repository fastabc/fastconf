//go:build !no_provider_http

// Package http is a first-party HTTP/HTTPS provider for FastConf.
//
// It performs periodic GETs against a configured URL, decodes the body
// using a registered FastConf Codec (yaml/json by default), and emits
// change events to drive Manager reloads. The implementation is the
// canonical "remote provider golden reference" for FastConf:
//
//   - Stateless: no goroutines outside the user-controlled Watch loop.
//   - ETag / If-None-Match aware: when the server supplies an ETag
//     header, subsequent polls send If-None-Match and treat 304 as
//     "unchanged" (no event, no reload work).
//   - Body-hash fallback: if the server omits ETag, the provider
//     hashes the response body and only emits an event when the hash
//     changes — preserves FastConf's "no spurious reload" guarantee.
//   - Pluggable HTTP client and clock for tests; default uses
//     http.DefaultClient with a 10s timeout.
//   - Implements contracts.Provider so it slots into
//     fastconf.WithProvider(...) directly.
//
// The module is intentionally tiny and depends only on the standard
// library plus fastconf/contracts so it can be vendored as a reference
// when authoring proprietary remote providers.
package http

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// Codec is the minimal byte→map decoder this provider needs. It
// matches contracts.Codec exactly so the user can pass any registered
// FastConf codec (yaml, json, toml-via-plugin, ...) without adapter.
type Codec = contracts.Codec

// Doer is the subset of *http.Client needed by Provider — anything
// implementing this contract can be injected for tests, retries, or
// authenticated transports.
type Doer interface {
	Do(req *nethttp.Request) (*nethttp.Response, error)
}

// Provider polls an HTTP endpoint on a configurable interval and
// emits a change event whenever the response payload (or its ETag)
// differs from the last accepted one.
type Provider struct {
	name     string
	url      string
	priority int
	codec    Codec
	client   Doer
	interval time.Duration
	headers  map[string]string

	mu       sync.Mutex
	etag     string
	bodyHash [32]byte
	loaded   bool
	lastBody map[string]any
}

// Option mutates a Provider during construction. Use the With* helpers
// below to compose configuration without growing New's signature.
type Option func(*Provider)

// WithPriority overrides the default priority (PriorityKV).
func WithPriority(p int) Option { return func(pr *Provider) { pr.priority = p } }

// WithClient injects an alternate HTTP client (e.g. one with auth or
// instrumented for traces). Default: http.DefaultClient.
func WithClient(c Doer) Option { return func(pr *Provider) { pr.client = c } }

// WithInterval overrides the watch poll interval (default: 30s).
func WithInterval(d time.Duration) Option { return func(pr *Provider) { pr.interval = d } }

// WithHeader adds a static request header (Bearer tokens, tenant IDs,
// ...). Multiple calls accumulate.
func WithHeader(k, v string) Option {
	return func(pr *Provider) {
		if pr.headers == nil {
			pr.headers = map[string]string{}
		}
		pr.headers[k] = v
	}
}

// New constructs an HTTP-backed Provider. name is surfaced in
// Snapshot().Sources and metrics labels; codec decodes the response
// body. url MUST be absolute. A zero-value codec or empty url returns
// an error rather than panicking later in the reload loop.
func New(name, url string, codec Codec, opts ...Option) (*Provider, error) {
	if name == "" {
		return nil, errors.New("fastconf/http: provider name is required")
	}
	if url == "" {
		return nil, errors.New("fastconf/http: url is required")
	}
	if codec == nil {
		return nil, errors.New("fastconf/http: codec is required")
	}
	p := &Provider{
		name:     name,
		url:      url,
		priority: contracts.PriorityKV,
		codec:    codec,
		client:   &nethttp.Client{Timeout: 10 * time.Second},
		interval: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.name }

// Priority implements contracts.Provider.
func (p *Provider) Priority() int { return p.priority }

// Load fetches the URL and returns the decoded payload. It updates
// the provider's ETag / body-hash bookkeeping so the next Watch tick
// can short-circuit unchanged responses. A 304 response returns the
// previously-loaded snapshot rather than an error.
func (p *Provider) Load(ctx context.Context) (map[string]any, error) {
	body, etag, notModified, err := p.fetch(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if notModified && p.loaded {
		return cloneMap(p.lastBody), nil
	}
	hash := sha256.Sum256(body)
	if p.loaded && hash == p.bodyHash {
		return cloneMap(p.lastBody), nil
	}
	out, derr := p.codec.Decode(body)
	if derr != nil {
		return nil, fmt.Errorf("fastconf/http: decode %s: %w", p.url, derr)
	}
	p.etag = etag
	p.bodyHash = hash
	p.lastBody = out
	p.loaded = true
	return cloneMap(out), nil
}

// Watch ticks every interval (default 30s) and emits an event when the
// remote payload changes. Returning a closed channel on ctx cancel
// keeps the manager's watcher loop tidy.
func (p *Provider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	out := make(chan contracts.Event, 1)
	go func() {
		defer close(out)
		t := time.NewTicker(p.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if changed, _ := p.poll(ctx); changed {
					select {
					case out <- contracts.Event{Source: p.name, Reason: "http-poll-diff", At: time.Now()}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return out, nil
}

// poll performs a probe HTTP call without mutating decoded state. It
// returns true when the body or ETag has actually changed since the
// last accepted response — this is what suppresses spurious reloads.
func (p *Provider) poll(ctx context.Context) (bool, error) {
	body, etag, notModified, err := p.fetch(ctx)
	if err != nil {
		return false, err
	}
	if notModified {
		return false, nil
	}
	hash := sha256.Sum256(body)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded && hash == p.bodyHash && etag == p.etag {
		return false, nil
	}
	// Stash the raw signal but defer decode to Load() — Watch is a
	// notifier, not a decoder.
	p.bodyHash = hash
	p.etag = etag
	return true, nil
}

func (p *Provider) fetch(ctx context.Context) (body []byte, etag string, notModified bool, err error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, p.url, nil)
	if err != nil {
		return nil, "", false, err
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
	p.mu.Lock()
	if p.etag != "" {
		req.Header.Set("If-None-Match", p.etag)
	}
	p.mu.Unlock()
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == nethttp.StatusNotModified:
		return nil, resp.Header.Get("ETag"), true, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		b, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			return nil, "", false, rerr
		}
		return b, resp.Header.Get("ETag"), false, nil
	default:
		return nil, "", false, fmt.Errorf("fastconf/http: unexpected status %d for %s", resp.StatusCode, p.url)
	}
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
