// Package redisstream implements a FastConf Provider backed by a Redis
// Streams compatible client.
//
// Like the sibling `providers/nats` module, this package does NOT
// depend on github.com/redis/go-redis directly — it only requires the
// small set of methods FastConf actually uses via the Client interface
// below. Users wire in their real Redis client through a tiny adapter
// (see ExampleAdapter in redisstream_test.go).
//
// # Watch model
//
// The Provider issues XREAD BLOCK against the stream key and forwards
// every received entry as a contracts.Event. Each Redis entry's stream
// id ("1707332450123-0") doubles as the revision token so resumed
// subscriptions can pick up exactly where they left off via WatchFrom.
// Slow downstream consumers cause messages to be dropped on the
// outbound channel (drop-on-full) — FastConf's single-writer reload
// loop preserves order.
package redisstream

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// Entry is one Redis Streams entry, normalised to what the provider
// needs. The "payload" field name is configurable via WithPayloadField.
type Entry struct {
	ID     string            // stream id, e.g. "1707332450123-0"
	Fields map[string]string // raw field map from XREAD
}

// Client is the subset of *redis.Client FastConf needs. Implementations
// MUST honour the context for cancel and SHOULD block up to `block`
// before returning an empty slice.
type Client interface {
	// XRead blocks until at least one entry is available on `stream`
	// after `lastID`, or until `block` elapses, or until ctx is done.
	// Returning (nil, ctx.Err()) on cancel is expected.
	XRead(ctx context.Context, stream, lastID string, block time.Duration) ([]Entry, error)
}

// Codec decodes the entry's payload bytes to a generic map.
type Codec = contracts.Codec

// Provider implements contracts.Provider and contracts.Resumable.
type Provider struct {
	name         string
	stream       string
	priority     int
	codec        Codec
	client       Client
	block        time.Duration
	payloadField string

	mu       sync.RWMutex
	last     map[string]any
	lastID   string
	hasFirst bool
	dropped  uint64
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithPriority overrides the default priority (PriorityKV).
func WithPriority(p int) Option { return func(pr *Provider) { pr.priority = p } }

// WithBlock overrides the XREAD block duration (default: 5s).
func WithBlock(d time.Duration) Option { return func(pr *Provider) { pr.block = d } }

// WithPayloadField selects which entry field carries the encoded
// document (default: "payload"). Implementations using a different
// schema (e.g. "data") override here.
func WithPayloadField(name string) Option {
	return func(pr *Provider) { pr.payloadField = name }
}

// New constructs a Redis-Streams-backed Provider. stream is the Redis
// stream key (e.g. "fastconf:app"); codec decodes the entry payload
// bytes; client injects the Redis-like transport.
func New(name, stream string, codec Codec, client Client, opts ...Option) (*Provider, error) {
	if name == "" {
		return nil, errors.New("fastconf/redisstream: provider name is required")
	}
	if stream == "" {
		return nil, errors.New("fastconf/redisstream: stream is required")
	}
	if codec == nil {
		return nil, errors.New("fastconf/redisstream: codec is required")
	}
	if client == nil {
		return nil, errors.New("fastconf/redisstream: client is required")
	}
	p := &Provider{
		name:         name,
		stream:       stream,
		priority:     contracts.PriorityKV,
		codec:        codec,
		client:       client,
		block:        5 * time.Second,
		payloadField: "payload",
		last:         map[string]any{},
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

// Load returns the most recent decoded snapshot (empty map until the
// first message arrives).
func (p *Provider) Load(_ context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]any, len(p.last))
	for k, v := range p.last {
		out[k] = v
	}
	return out, nil
}

// LoadSnapshot exposes Revision and a Stale flag.
func (p *Provider) LoadSnapshot(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.Load(ctx)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return contracts.Snapshot{Map: m, Revision: p.lastID, Stale: !p.hasFirst}, nil
}

// Dropped reports messages dropped because the outbound channel was full.
func (p *Provider) Dropped() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dropped
}

// Watch implements contracts.Provider. Starts reading from the tail of
// the stream ("$"), forwarding only entries added after subscribe.
func (p *Provider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	return p.loop(ctx, "$")
}

// WatchFrom implements contracts.Resumable. Re-subscribes from the
// supplied lastRev (stream id). Empty lastRev behaves like Watch.
func (p *Provider) WatchFrom(ctx context.Context, lastRev string) (<-chan contracts.Event, error) {
	if lastRev == "" {
		lastRev = "$"
	}
	return p.loop(ctx, lastRev)
}

func (p *Provider) loop(ctx context.Context, startID string) (<-chan contracts.Event, error) {
	out := make(chan contracts.Event, 16)
	go func() {
		defer close(out)
		lastID := startID
		for {
			if ctx.Err() != nil {
				return
			}
			entries, err := p.client.XRead(ctx, p.stream, lastID, p.block)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// transient error — back off briefly to avoid tight spin
				select {
				case <-time.After(250 * time.Millisecond):
				case <-ctx.Done():
					return
				}
				continue
			}
			for _, e := range entries {
				lastID = e.ID
				raw, ok := e.Fields[p.payloadField]
				if !ok {
					continue
				}
				m, derr := p.codec.Decode([]byte(raw))
				if derr != nil {
					continue
				}
				p.mu.Lock()
				p.last = m
				p.lastID = e.ID
				p.hasFirst = true
				p.mu.Unlock()
				ev := contracts.Event{
					Source:   p.name,
					Reason:   "redis-streams",
					Revision: e.ID,
					At:       time.Now(),
				}
				select {
				case out <- ev:
				default:
					p.mu.Lock()
					p.dropped++
					p.mu.Unlock()
				}
			}
		}
	}()
	return out, nil
}

// Compile-time interface assertions.
var (
	_ contracts.Provider         = (*Provider)(nil)
	_ contracts.SnapshotProvider = (*Provider)(nil)
	_ contracts.Resumable        = (*Provider)(nil)
)
