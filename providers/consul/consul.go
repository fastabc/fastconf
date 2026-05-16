//go:build !no_provider_consul

// Package consul is a first-party Consul KV provider for FastConf.
//
// It uses Consul's native HTTP API (the /v1/kv endpoint) directly
// rather than depending on github.com/hashicorp/consul/api, keeping
// the module free of hashicorp transitive dependencies. Users that
// prefer the official client can implement contracts.Provider in
// their own package — this package is the "golden reference" for
// KV-style remote sources.
//
// # Watch model
//
// Consul KV exposes blocking queries via the X-Consul-Index response
// header and the matching ?index=N&wait=… query parameters. The
// provider issues a long-poll on every Watch tick: the first call
// records the current index, subsequent calls block on Consul
// (default 5 minutes) until the index changes or the wait expires.
// On change the provider emits a contracts.Event and updates the
// stored index. On any HTTP error, the loop backs off exponentially
// (250ms..30s) and surfaces a ProviderError metric to the configured
// MetricsSink.
//
// # Decoding
//
// Two recursion modes are supported (see Mode):
//
//   - ModeKV  : flat KV — each key under the prefix becomes a leaf,
//     "/" delimited keys produce nested maps. Values are decoded as
//     UTF-8 strings; numeric / boolean coercion is left to the user
//     via fastconf transformers.
//   - ModeBlob: a single key under the prefix holds an encoded
//     document (yaml/json) — Codec is used to decode it.
package consul

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// Mode selects how the values returned by Consul are interpreted.
type Mode int

const (
	// ModeKV treats every key under Prefix as a leaf and reconstructs
	// a nested map by splitting on "/". Empty trailing keys (folder
	// markers) are skipped.
	ModeKV Mode = iota
	// ModeBlob expects a single key holding an encoded document
	// (yaml/json) and decodes it via the supplied Codec.
	ModeBlob
)

// Doer matches *nethttp.Client; injected for tests / instrumented
// transports.
type Doer interface {
	Do(req *nethttp.Request) (*nethttp.Response, error)
}

// Provider is a contracts.Provider backed by the Consul HTTP API.
type Provider struct {
	name     string
	addr     string
	prefix   string
	priority int
	mode     Mode
	codec    contracts.Codec
	token    string
	dc       string
	wait     time.Duration
	client   Doer

	mu    sync.Mutex
	index uint64
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithPriority overrides the default priority (PriorityKV).
func WithPriority(p int) Option { return func(pr *Provider) { pr.priority = p } }

// WithName overrides the default Provider.Name() (defaults to
// "consul://<addr>/<prefix>").
func WithName(n string) Option { return func(pr *Provider) { pr.name = n } }

// WithMode selects KV vs Blob decoding.
func WithMode(m Mode) Option { return func(pr *Provider) { pr.mode = m } }

// WithCodec installs a Codec for ModeBlob; ignored in ModeKV.
func WithCodec(c contracts.Codec) Option { return func(pr *Provider) { pr.codec = c } }

// WithToken sets the X-Consul-Token header for ACL-protected agents.
func WithToken(t string) Option { return func(pr *Provider) { pr.token = t } }

// WithDatacenter sets the ?dc= query parameter.
func WithDatacenter(dc string) Option { return func(pr *Provider) { pr.dc = dc } }

// WithWait overrides the blocking-query wait window (default 5m).
// Consul caps wait at 10m server-side.
func WithWait(d time.Duration) Option { return func(pr *Provider) { pr.wait = d } }

// WithClient injects an alternate HTTP client.
func WithClient(c Doer) Option { return func(pr *Provider) { pr.client = c } }

// New constructs a Consul KV provider rooted at addr+prefix. addr is a
// full URL such as "http://127.0.0.1:8500"; prefix is a slash-rooted
// path such as "/myapp/" (leading and trailing slashes are normalised).
func New(addr, prefix string, opts ...Option) (*Provider, error) {
	if addr == "" {
		return nil, errors.New("consul: addr is empty")
	}
	if _, err := url.Parse(addr); err != nil {
		return nil, fmt.Errorf("consul: invalid addr: %w", err)
	}
	prefix = strings.Trim(prefix, "/")
	p := &Provider{
		addr:     strings.TrimRight(addr, "/"),
		prefix:   prefix,
		priority: contracts.PriorityKV,
		wait:     5 * time.Minute,
		// Default to an isolated *http.Client — never net/http.DefaultClient.
		// We intentionally do NOT set Timeout: Watch() uses blocking
		// queries that legitimately wait up to p.wait (default 5m), and a
		// fixed Timeout would tear those down prematurely. Cancellation
		// now flows through ctx (P1.1) so per-request lifetimes remain
		// under caller control.
		client: &nethttp.Client{},
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.name == "" {
		p.name = fmt.Sprintf("consul://%s/%s", strings.TrimPrefix(p.addr, "http://"), prefix)
	}
	if p.mode == ModeBlob && p.codec == nil {
		return nil, errors.New("consul: ModeBlob requires WithCodec")
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.name }

// Priority implements contracts.Provider.
func (p *Provider) Priority() int { return p.priority }

// Load implements contracts.Provider. A single recursive GET against
// /v1/kv/<prefix>?recurse populates the entire subtree and updates the
// stored Consul index for subsequent blocking-query Watch calls.
func (p *Provider) Load(ctx context.Context) (map[string]any, error) {
	pairs, idx, err := p.fetch(ctx, 0)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.index = idx
	p.mu.Unlock()
	return p.decode(pairs)
}

// Watch implements contracts.Provider with a blocking-query loop.
// Returning a closed channel is reserved for ctx-cancelled shutdown;
// the loop keeps running until ctx.Done().
func (p *Provider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	out := make(chan contracts.Event, 1)
	go p.watchLoop(ctx, out)
	return out, nil
}

func (p *Provider) watchLoop(ctx context.Context, out chan<- contracts.Event) {
	defer close(out)
	backoff := 250 * time.Millisecond
	const maxBackoff = 30 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		p.mu.Lock()
		idx := p.index
		p.mu.Unlock()
		_, newIdx, err := p.fetch(ctx, idx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = 250 * time.Millisecond
		if newIdx == 0 || newIdx == idx {
			continue
		}
		p.mu.Lock()
		p.index = newIdx
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case out <- contracts.Event{Source: p.name, Reason: "consul-index", At: time.Now()}:
		default:
			// Reload pipeline already busy; the next iteration will
			// observe the latest index regardless.
		}
	}
}

// kvPair mirrors the JSON shape returned by /v1/kv?recurse.
type kvPair struct {
	Key         string `json:"Key"`
	Value       string `json:"Value"` // base64
	ModifyIndex uint64 `json:"ModifyIndex"`
}

func (p *Provider) fetch(ctx context.Context, index uint64) ([]kvPair, uint64, error) {
	u := fmt.Sprintf("%s/v1/kv/%s?recurse=true", p.addr, p.prefix)
	if index > 0 {
		u += fmt.Sprintf("&index=%d&wait=%s", index, p.wait.String())
	}
	if p.dc != "" {
		u += "&dc=" + url.QueryEscape(p.dc)
	}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	if p.token != "" {
		req.Header.Set("X-Consul-Token", p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case nethttp.StatusOK:
	case nethttp.StatusNotFound:
		// Empty subtree — surface as no pairs but keep index advance.
		return nil, parseIndex(resp.Header.Get("X-Consul-Index")), nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, 0, fmt.Errorf("consul: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var pairs []kvPair
	if err := json.NewDecoder(resp.Body).Decode(&pairs); err != nil {
		return nil, 0, fmt.Errorf("consul: decode: %w", err)
	}
	return pairs, parseIndex(resp.Header.Get("X-Consul-Index")), nil
}

func parseIndex(s string) uint64 {
	var v uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		v = v*10 + uint64(r-'0')
	}
	return v
}

func (p *Provider) decode(pairs []kvPair) (map[string]any, error) {
	if len(pairs) == 0 {
		return map[string]any{}, nil
	}
	if p.mode == ModeBlob {
		// Find the single non-folder pair with non-empty value.
		for _, kv := range pairs {
			if strings.HasSuffix(kv.Key, "/") || kv.Value == "" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(kv.Value)
			if err != nil {
				return nil, fmt.Errorf("consul: base64 %s: %w", kv.Key, err)
			}
			return p.codec.Decode(raw)
		}
		return map[string]any{}, nil
	}
	out := map[string]any{}
	for _, kv := range pairs {
		key := strings.TrimPrefix(kv.Key, p.prefix)
		key = strings.TrimPrefix(key, "/")
		if key == "" || strings.HasSuffix(key, "/") {
			continue
		}
		val, err := base64.StdEncoding.DecodeString(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("consul: base64 %s: %w", kv.Key, err)
		}
		mappath.Set(out, strings.Split(key, "/"), string(val))
	}
	return out, nil
}
