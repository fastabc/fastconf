// Package redisstream demonstrates a streaming provider using an injected
// Redis-like client. Watch uses XREAD BLOCK; entry IDs act as resume revisions.
// Decoded documents are cached and events offered without blocking.
// This is a reference implementation; supply an adapter for the real client.
package redisstream

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
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

// Provider implements contracts.Provider and contracts.Describer.
type Provider struct {
	name         string
	stream       string
	priority     int
	codec        Codec
	client       Client
	block        time.Duration
	payloadField string

	latest  providerutil.Latest
	dropped atomic.Uint64
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

// Load returns the most recent decoded snapshot (an empty, Stale map until
// the first entry arrives) with the last stream id as Revision.
func (p *Provider) Load(_ context.Context) (contracts.Snapshot, error) {
	return p.latest.Snapshot(), nil
}

// Dropped reports messages dropped because the outbound channel was full.
func (p *Provider) Dropped() uint64 {
	return p.dropped.Load()
}

// Watch implements contracts.Provider. An empty from starts at the tail of
// the stream ("$"), forwarding only entries added after subscribe; a stream
// id resumes strictly after that entry.
func (p *Provider) Watch(ctx context.Context, from string) (<-chan contracts.Event, error) {
	if from == "" {
		from = "$"
	}
	return p.loop(ctx, from)
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
				p.latest.Store(m, e.ID)
				ev := contracts.Event{
					Source:   p.name,
					Reason:   "redis-streams",
					Revision: e.ID,
					At:       time.Now(),
				}
				if !providerutil.Offer(out, ev) {
					p.dropped.Add(1)
				}
			}
		}
	}()
	return out, nil
}

// Compile-time interface assertions.
var (
	_ contracts.Provider  = (*Provider)(nil)
	_ contracts.Describer = (*Provider)(nil)
)
