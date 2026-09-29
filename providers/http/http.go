// Package http loads remote documents and polls for changes. Accepted ETags enable conditional
// GETs; body hashes detect changes when no ETag is provided. The default client has a 10s timeout
// and rejects redirects to protect headers.
package http

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	nethttp "net/http"
	"sync"
	"time"

	registry "github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// Codec is the document decoder accepted by New.
type Codec = contracts.Codec

// Doer permits custom clients, authenticated transports and test doubles.
type Doer = providerutil.Doer

// Provider polls an HTTP endpoint on a configurable interval and emits a change event whenever the
// response payload (or its ETag) differs from the last accepted one.
type Provider struct {
	name         string
	url          string
	priority     int
	codec        Codec
	client       Doer
	interval     time.Duration
	headers      map[string]string
	maxBodyBytes int64

	mu       sync.Mutex
	etag     string
	bodyHash [32]byte
	loaded   bool
	lastBody map[string]any
}

// Option mutates a Provider during construction. Use the With* helpers below to compose
// configuration without growing New's signature.
type Option func(*Provider)

// WithPriority overrides the default priority (PriorityKV).
func WithPriority(p int) Option { return func(pr *Provider) { pr.priority = p } }

// WithClient injects an alternate HTTP client (e.g. one with auth or instrumented for traces). The
// default has a 10s timeout and rejects redirects.
func WithClient(c Doer) Option { return func(pr *Provider) { pr.client = c } }

// WithInterval sets the poll interval (default: 30s); zero disables polling.
func WithInterval(d time.Duration) Option { return func(pr *Provider) { pr.interval = d } }

// WithMaxBodyBytes limits successful response bodies. Zero restores the 4 MiB default. The reader
// consumes one extra byte to reject truncation.
func WithMaxBodyBytes(n int64) Option { return func(pr *Provider) { pr.maxBodyBytes = n } }

// WithHeader adds a static request header (Bearer tokens, tenant IDs, ...). Multiple calls
// accumulate.
func WithHeader(k, v string) Option {
	return func(pr *Provider) {
		if pr.headers == nil {
			pr.headers = map[string]string{}
		}
		pr.headers[k] = v
	}
}

// New constructs an HTTP-backed Provider. name is surfaced in Snapshot().Sources and metrics
// labels; codec decodes the response body. A nil codec selects the registered codec by response
// Content-Type. Empty names and URLs return an error.
func New(name, url string, codec Codec, opts ...Option) (*Provider, error) {
	if name == "" {
		return nil, errors.New("fastconf/http: provider name is required")
	}
	if url == "" {
		return nil, errors.New("fastconf/http: url is required")
	}
	p := &Provider{
		name:         name,
		url:          url,
		priority:     contracts.PriorityKV,
		codec:        codec,
		client:       providerutil.HTTPClient(10 * time.Second),
		interval:     30 * time.Second,
		maxBodyBytes: contracts.DefaultMaxBodyBytes,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load fetches the URL and returns the decoded payload. It updates the provider's ETag / body-hash
// bookkeeping so the next Watch tick can short-circuit unchanged responses. A 304 response returns
// the previously-loaded snapshot rather than an error.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	body, etag, contentType, notModified, err := p.fetch(ctx)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if notModified {
		if !p.loaded {
			return contracts.Snapshot{}, fmt.Errorf("fastconf/http: 304 without an accepted document")
		}
		return contracts.Snapshot{Map: maps.Clone(p.lastBody), Revision: p.etag}, nil
	}
	hash := sha256.Sum256(body)
	if p.loaded && hash == p.bodyHash {
		p.etag = etag
		return contracts.Snapshot{Map: maps.Clone(p.lastBody), Revision: p.etag}, nil
	}
	decoder := p.codec
	if decoder == nil {
		var ok bool
		decoder, ok = registry.ByContentType(contentType)
		if !ok {
			return contracts.Snapshot{}, fmt.Errorf("fastconf/http: %w: content-type %q", registry.ErrUnknownCodec, contentType)
		}
	}
	out, derr := decoder.Decode(body)
	if derr != nil {
		return contracts.Snapshot{}, fmt.Errorf("fastconf/http: decode %s: %w", p.url, derr)
	}
	p.etag = etag
	p.bodyHash = hash
	p.lastBody = out
	p.loaded = true
	return contracts.Snapshot{Map: maps.Clone(out), Revision: p.etag}, nil
}

// Watch ticks every interval (default 30s) and emits an event when the remote payload changes.
// Returning a closed channel on ctx cancel keeps the manager's watcher loop tidy.
func (p *Provider) Watch(ctx context.Context, _ string) (<-chan contracts.Event, error) {
	if p.interval <= 0 {
		return nil, nil
	}
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

// poll performs a probe HTTP call without mutating decoded state. It returns true when the body or
// ETag has actually changed since the last accepted response — this is what suppresses spurious
// reloads.
func (p *Provider) poll(ctx context.Context) (bool, error) {
	body, etag, _, notModified, err := p.fetch(ctx)
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
	// Only a successful Load may advance the accepted body/hash/ETag.
	// Retain the signal until a reader accepts it, including after decode errors.
	return true, nil
}

func (p *Provider) fetch(ctx context.Context) (body []byte, etag, contentType string, notModified bool, err error) {
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, p.url, nil)
	if err != nil {
		return nil, "", "", false, err
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
		return nil, "", "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == nethttp.StatusNotModified:
		return nil, resp.Header.Get("ETag"), "", true, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		b, rerr := providerutil.ReadAllMax(resp.Body, p.maxBodyBytes)
		if rerr != nil {
			return nil, "", "", false, rerr
		}
		return b, resp.Header.Get("ETag"), resp.Header.Get("Content-Type"), false, nil
	default:
		return nil, "", "", false, fmt.Errorf("fastconf/http: unexpected status %d for %s", resp.StatusCode, p.url)
	}
}
