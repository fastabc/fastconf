//go:build !no_provider_vault

// Package vault is a first-party HashiCorp Vault KV v2 provider for
// FastConf.
//
// It uses Vault's HTTP API directly (no hashicorp/vault/api dependency),
// keeping the module free of large transitive trees.
// The implementation targets the most common operator pattern:
//
//   - KV v2 secret engine mounted at a known path (default "secret").
//   - A single secret holding a flat string→string map. Nested
//     structure is reconstructed by splitting keys on a configurable
//     separator (default ".") so that operators can store
//     "database.dsn" = "postgres://…" and FastConf will produce
//     {database: {dsn: "..."}} for the merge stage.
//   - Token auth via the X-Vault-Token header. Lease renewal /
//     AppRole / Kubernetes auth are deliberately out of scope for
//     this reference module — wrap a custom Doer for those flows.
//
// Watch polls the secret metadata endpoint every Interval and emits
// a contracts.Event whenever current_version changes. This costs one
// tiny HTTP request per Interval and keeps the implementation
// dependency-free; for sub-second freshness use the official client.
package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// Doer matches *nethttp.Client; injected for tests / instrumented
// transports / Vault Agent sidecars.
type Doer interface {
	Do(req *nethttp.Request) (*nethttp.Response, error)
}

// Provider implements contracts.Provider for KV v2 secrets.
type Provider struct {
	name      string
	addr      string
	mount     string
	path      string
	token     string
	priority  int
	separator string
	interval  time.Duration
	client    Doer

	mu      sync.Mutex
	version int

	// Pluggable auth + lease renewal: see Auth interface + renewer goroutine.
	auth        Auth
	renewBefore time.Duration
	renewOnce   sync.Once
	tokenTTL    atomic.Int64
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithName overrides the default Provider name.
func WithName(n string) Option { return func(p *Provider) { p.name = n } }

// WithPriority overrides the default priority (PriorityKV).
func WithPriority(pp int) Option { return func(p *Provider) { p.priority = pp } }

// WithMount overrides the KV v2 mount path (default "secret").
func WithMount(m string) Option {
	return func(p *Provider) { p.mount = strings.Trim(m, "/") }
}

// WithSeparator changes the key splitter used to reconstruct nested
// maps from flat KV string keys (default ".").
func WithSeparator(s string) Option { return func(p *Provider) { p.separator = s } }

// WithInterval sets the metadata-poll interval used by Watch
// (default 30s). Set to 0 to disable Watch (Watch returns nil channel).
func WithInterval(d time.Duration) Option { return func(p *Provider) { p.interval = d } }

// WithClient injects an alternate HTTP client.
func WithClient(c Doer) Option { return func(p *Provider) { p.client = c } }

// New constructs a Vault KV v2 provider rooted at addr+mount+path.
// addr is a full URL such as "https://vault.example.com:8200"; path
// is the secret path under data/, e.g. "myapp/config".
func New(addr, path, token string, opts ...Option) (*Provider, error) {
	if addr == "" {
		return nil, errors.New("vault: addr is empty")
	}
	if path == "" {
		return nil, errors.New("vault: path is empty")
	}
	if token == "" && len(opts) == 0 {
		return nil, errors.New("vault: token is empty (use WithAuth for dynamic tokens)")
	}
	p := &Provider{
		addr:      strings.TrimRight(addr, "/"),
		mount:     "secret",
		path:      strings.Trim(path, "/"),
		token:     token,
		priority:  contracts.PriorityKV,
		separator: ".",
		interval:  30 * time.Second,
		client:    &nethttp.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.name == "" {
		p.name = fmt.Sprintf("vault://%s/%s", p.mount, p.path)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.name }

// Priority implements contracts.Provider.
func (p *Provider) Priority() int { return p.priority }

// Load implements contracts.Provider. A single GET against the KV v2
// data endpoint returns the secret payload and current version; the
// version is recorded so Watch can detect future rotations.
func (p *Provider) Load(ctx context.Context) (map[string]any, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, err
	}
	data, version, err := p.readData(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.version = version
	p.mu.Unlock()
	return p.expand(data), nil
}

// Watch implements contracts.Provider via metadata polling. Returns a
// nil channel when WithInterval(0) was used.
func (p *Provider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, err
	}
	if p.interval == 0 && p.auth == nil {
		return nil, nil
	}
	out := make(chan contracts.Event, 4)
	if p.auth != nil {
		p.startAuthRenewer(ctx, out)
	}
	if p.interval > 0 {
		go p.watchLoop(ctx, out)
	} else {
		// Interval=0 + auth: only the renewer keeps the channel alive.
		// Why: explicitly close `out` when ctx ends so the subscribe()
		// drain goroutine in fastconf observes clean termination instead
		// of leaking. The renewer goroutine handles its own exit
		// independently.
		go p.keepaliveLoop(ctx, out)
	}
	return out, nil
}

// keepaliveLoop holds the Watch channel reference alive for interval=0
// + auth deployments. The channel is intentionally NOT closed on exit:
// the renewer goroutine may still write to it, and closing here would
// race with that send. The framework's consumeProviderEvents drains the
// channel under its own ctx select, so the abandoned-channel pattern is
// safe — the receive side unblocks via ctx.Done() rather than via a
// close signal.
func (p *Provider) keepaliveLoop(ctx context.Context, out chan<- contracts.Event) {
	<-ctx.Done()
	_ = out
}

func (p *Provider) watchLoop(ctx context.Context, out chan<- contracts.Event) {
	tick := time.NewTicker(p.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		v, err := p.readMetadataVersion(ctx)
		if err != nil || v == 0 {
			continue
		}
		p.mu.Lock()
		old := p.version
		p.mu.Unlock()
		if v == old {
			continue
		}
		// Only commit the new version when the event is actually
		// delivered. A dropped event without a version rollback would
		// permanently mask the update: the next tick would see v==old
		// and skip re-emit until the remote bumped the version again.
		select {
		case <-ctx.Done():
			return
		case out <- contracts.Event{Source: p.name, Reason: "vault-version", At: time.Now()}:
			p.mu.Lock()
			p.version = v
			p.mu.Unlock()
		default:
			// Consumer back-pressure: leave p.version alone so the
			// next tick retries the send.
		}
	}
}

// Remove providerAuth (unused) — fields are now on Provider directly.

// kvData mirrors /v1/<mount>/data/<path> response shape.
type kvData struct {
	Data struct {
		Data     map[string]any `json:"data"`
		Metadata struct {
			Version int `json:"version"`
		} `json:"metadata"`
	} `json:"data"`
}

// loadToken returns the current Vault token under p.mu so concurrent
// renews in renewLoop / ensureToken cannot race the HTTP request path.
// Go strings are two words; an unprotected read can tear during rotation.
func (p *Provider) loadToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}

func (p *Provider) readData(ctx context.Context) (map[string]any, int, error) {
	url := fmt.Sprintf("%s/v1/%s/data/%s", p.addr, p.mount, p.path)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Vault-Token", p.loadToken())
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, 0, fmt.Errorf("vault: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out kvData
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("vault: decode: %w", err)
	}
	return out.Data.Data, out.Data.Metadata.Version, nil
}

type kvMetadata struct {
	Data struct {
		CurrentVersion int `json:"current_version"`
	} `json:"data"`
}

func (p *Provider) readMetadataVersion(ctx context.Context) (int, error) {
	url := fmt.Sprintf("%s/v1/%s/metadata/%s", p.addr, p.mount, p.path)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Vault-Token", p.loadToken())
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		return 0, fmt.Errorf("vault metadata: status %d", resp.StatusCode)
	}
	var out kvMetadata
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.Data.CurrentVersion, nil
}

// expand reconstructs nested maps from flat keys split on separator.
// "database.dsn" → {database: {dsn: …}}; collisions are resolved by
// preferring the deeper write (Vault keys are unique, so this only
// matters when an operator stores both "a" and "a.b"). Keys are
// processed in ascending segment-count order so deeper paths always
// win regardless of map-iteration order.
func (p *Provider) expand(in map[string]any) map[string]any {
	if p.separator == "" || len(in) == 0 {
		out := make(map[string]any, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	// Collect and sort keys by segment count (ascending) so that
	// shallow writes happen first and deeper writes reliably overwrite
	// them, producing deterministic output across reloads.
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ci := strings.Count(keys[i], p.separator)
		cj := strings.Count(keys[j], p.separator)
		if ci != cj {
			return ci < cj
		}
		return keys[i] < keys[j]
	})
	out := map[string]any{}
	for _, k := range keys {
		v := in[k]
		parts := strings.Split(k, p.separator)
		cur := out
		for i, seg := range parts {
			if i == len(parts)-1 {
				cur[seg] = v
				break
			}
			next, ok := cur[seg].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[seg] = next
			}
			cur = next
		}
	}
	return out
}
