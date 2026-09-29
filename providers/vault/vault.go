// Package vault loads Vault KV v2 through its HTTP API. Flat keys expand using a configurable
// separator (default dot). X-Vault-Token authenticates requests; optional Auth supports re-login
// before expiry. Watch polls secret metadata for version changes and can also report token renewal
// events.
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
	"github.com/fastabc/fastconf/internal/providerutil"
)

// Doer matches *nethttp.Client; injected for tests / instrumented transports / Vault Agent
// sidecars.
type Doer = providerutil.Doer

// Provider implements contracts.Provider for KV v2 secrets.
type Provider struct {
	name         string
	addr         string
	mount        string
	path         string
	token        string
	priority     int
	separator    string
	interval     time.Duration
	maxBodyBytes int64
	client       Doer

	// version is the last version Load read successfully; notified is the
	// version Watch last announced and is cleared by every Load attempt, so
	// a failed or stale read is announced again on the next poll.
	mu       sync.Mutex
	version  int
	notified int

	// Pluggable auth + lease renewal: see Auth interface + renewer goroutine.
	auth        Auth
	renewBefore time.Duration
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

// WithSeparator changes the key splitter used to reconstruct nested maps from flat KV string keys
// (default ".").
func WithSeparator(s string) Option { return func(p *Provider) { p.separator = s } }

// WithInterval sets the metadata-poll interval used by Watch (default 30s). Set to 0 to disable
// metadata polling. Auth renewal may still emit events unless WithRenewBefore(0) also disables it.
func WithInterval(d time.Duration) Option { return func(p *Provider) { p.interval = d } }

// WithMaxBodyBytes limits successful KV and metadata response bodies. Zero restores the 4 MiB
// default and rejects oversized documents before decode.
func WithMaxBodyBytes(n int64) Option { return func(p *Provider) { p.maxBodyBytes = n } }

// WithClient injects an alternate HTTP client. The caller must enforce redirect and credential
// isolation; the supplied client is never modified.
func WithClient(c Doer) Option { return func(p *Provider) { p.client = c } }

// New constructs a Vault KV v2 provider rooted at addr+mount+path. addr is a full URL such as
// "https://vault.example.com:8200"; path is the secret path under data/, e.g. "myapp/config".
func New(addr, path, token string, opts ...Option) (*Provider, error) {
	if addr == "" {
		return nil, errors.New("vault: addr is empty")
	}
	if path == "" {
		return nil, errors.New("vault: path is empty")
	}
	p := &Provider{
		addr:         strings.TrimRight(addr, "/"),
		mount:        "secret",
		path:         strings.Trim(path, "/"),
		token:        token,
		priority:     contracts.PriorityKV,
		separator:    ".",
		interval:     30 * time.Second,
		maxBodyBytes: contracts.DefaultMaxBodyBytes,
		renewBefore:  30 * time.Second,
		client:       providerutil.HTTPClient(10 * time.Second),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.token == "" && p.auth == nil {
		return nil, errors.New("vault: token is empty (use WithAuth for dynamic tokens)")
	}
	if p.name == "" {
		p.name = fmt.Sprintf("vault://%s/%s", p.mount, p.path)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load implements contracts.Provider.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.loadMap(ctx)
	return contracts.Snapshot{Map: m}, err
}

// Load implements contracts.Provider. A single GET against the KV v2 data endpoint returns the
// secret payload and current version; the version is recorded so Watch can detect future
// rotations.
func (p *Provider) loadMap(ctx context.Context) (map[string]any, error) {
	p.mu.Lock()
	p.notified = 0
	p.mu.Unlock()
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

// Watch starts an independent polling/renewal session and closes its event channel after
// cancellation and all session publishers have stopped. It returns nil when both metadata polling
// and auth renewal are disabled.
func (p *Provider) Watch(ctx context.Context, _ string) (<-chan contracts.Event, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, err
	}
	if p.interval == 0 && (p.auth == nil || p.renewBefore <= 0) {
		return nil, nil
	}
	out := make(chan contracts.Event, 4)
	go func() {
		defer close(out)
		var publishers sync.WaitGroup
		if p.auth != nil && p.renewBefore > 0 {
			publishers.Add(1)
			go func() {
				defer publishers.Done()
				p.renewLoop(ctx, out)
			}()
		}
		if p.interval > 0 {
			p.watchLoop(ctx, out)
		} else {
			<-ctx.Done()
		}
		publishers.Wait()
	}()
	return out, nil
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
		// Delivering the event does not mean the version was accepted:
		// only a successful Load advances p.version. notified merely
		// suppresses duplicates until the triggered Load runs. It is set
		// before the send so that Load, which may run before the send
		// returns, always clears it afterwards.
		p.mu.Lock()
		skip := v == p.version || v == p.notified
		if !skip {
			p.notified = v
		}
		p.mu.Unlock()
		if skip {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case out <- contracts.Event{Source: p.name, Reason: "vault-version", At: time.Now()}:
		default:
			// Consumer back-pressure: retry the send on the next tick.
			p.mu.Lock()
			if p.notified == v {
				p.notified = 0
			}
			p.mu.Unlock()
		}
	}
}

// kvData mirrors /v1/<mount>/data/<path> response shape.
type kvData struct {
	Data struct {
		Data     map[string]any `json:"data"`
		Metadata struct {
			Version int `json:"version"`
		} `json:"metadata"`
	} `json:"data"`
}

// loadToken returns the current Vault token under p.mu so concurrent renews in renewLoop /
// ensureToken cannot race the HTTP request path. Go strings are two words; an unprotected read can
// tear during rotation.
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != nethttp.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, 0, fmt.Errorf("vault: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := providerutil.ReadAllMax(resp.Body, p.maxBodyBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("vault: read body: %w", err)
	}
	var out kvData
	if err := json.Unmarshal(body, &out); err != nil {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != nethttp.StatusOK {
		return 0, fmt.Errorf("vault metadata: status %d", resp.StatusCode)
	}
	body, err := providerutil.ReadAllMax(resp.Body, p.maxBodyBytes)
	if err != nil {
		return 0, fmt.Errorf("vault metadata: read body: %w", err)
	}
	var out kvMetadata
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	return out.Data.CurrentVersion, nil
}

// expand reconstructs nested maps from flat keys split on separator. "database.dsn" → {database:
// {dsn: …}}; collisions are resolved by preferring the deeper write (Vault keys are unique, so
// this only matters when an operator stores both "a" and "a.b"). Keys are processed in ascending
// segment-count order so deeper paths always win regardless of map-iteration order.
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
