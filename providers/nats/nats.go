// Package nats implements a FastConf Provider backed by a NATS-style
// publish/subscribe connection.
//
// The package intentionally does NOT depend on github.com/nats-io/nats.go
// directly — it only requires the small set of methods FastConf actually
// uses, exposed via the Conn interface. Users with the real nats.go
// client wire it in through a tiny adapter (see ExampleAdapter in
// nats_test.go) so this module stays dependency-free for everyone who
// only wants the contract.
//
// # Watch model
//
// On Watch, the Provider issues Subscribe(subject) against the Conn and
// forwards every received Msg as a contracts.Event. The decoded payload
// is cached so subsequent Load() calls return the most recently
// observed snapshot. Slow downstream consumers cause messages to be
// dropped on the channel (drop-on-full); FastConf's single-writer
// reload loop preserves order anyway.
//
// # Resumable
//
// When the underlying NATS deployment supports JetStream-style
// "DeliverByStartSequence", the Conn implementation can read the
// lastRev passed to WatchFrom and configure the subscription
// accordingly. Cold subscribes (empty lastRev) behave identically to
// Watch.
package nats

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
)

// Msg is the minimal NATS message shape the Provider needs.
type Msg struct {
	Subject  string
	Data     []byte
	Revision string // optional opaque sequence id (JetStream stream seq, etc.)
}

// Subscription is what Conn.Subscribe returns. Unsubscribe MUST stop
// delivery and is invoked when the Watch context is cancelled.
type Subscription interface {
	Unsubscribe() error
}

// Conn is the subset of *nats.Conn FastConf needs. Users wire in their
// own nats.go connection via a 5-line adapter.
type Conn interface {
	// Subscribe registers handler for subject. Implementations MUST
	// invoke handler from a separate goroutine (consistent with the
	// nats.go semantics) and return a Subscription whose Unsubscribe
	// stops delivery synchronously.
	Subscribe(subject string, handler func(Msg)) (Subscription, error)

	// SubscribeFrom is the optional resumable variant. Implementations
	// that cannot honour lastRev should return contracts.ErrResumeUnsupported.
	SubscribeFrom(subject, lastRev string, handler func(Msg)) (Subscription, error)
}

// Codec decodes a payload to a generic map.
type Codec = contracts.Codec

// Provider implements contracts.Provider and contracts.Resumable.
type Provider struct {
	name     string
	subject  string
	priority int
	codec    Codec
	conn     Conn

	mu       sync.RWMutex
	last     map[string]any
	lastRev  string
	lastAt   time.Time
	hasFirst bool

	// dropCounter is bumped each time Watch drops a message because the
	// outbound channel was full. Exposed via Dropped() for tests / metrics.
	dropped uint64
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithPriority overrides the default priority (PriorityKV).
func WithPriority(p int) Option { return func(pr *Provider) { pr.priority = p } }

// New constructs a NATS-backed Provider. subject identifies the topic
// to subscribe to (e.g. "fastconf.app"). codec decodes message payloads
// (yaml/json/...). conn injects the NATS-like transport.
func New(name, subject string, codec Codec, conn Conn, opts ...Option) (*Provider, error) {
	if name == "" {
		return nil, errors.New("fastconf/nats: provider name is required")
	}
	if subject == "" {
		return nil, errors.New("fastconf/nats: subject is required")
	}
	if codec == nil {
		return nil, errors.New("fastconf/nats: codec is required")
	}
	if conn == nil {
		return nil, errors.New("fastconf/nats: conn is required")
	}
	p := &Provider{
		name:     name,
		subject:  subject,
		priority: contracts.PriorityKV,
		codec:    codec,
		conn:     conn,
		last:     map[string]any{},
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

// LoadSnapshot exposes Revision and a Stale flag (Stale=true until the
// first message arrives).
func (p *Provider) LoadSnapshot(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.Load(ctx)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return contracts.Snapshot{Map: m, Revision: p.lastRev, Stale: !p.hasFirst}, nil
}

// Watch implements contracts.Provider.
func (p *Provider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	return p.subscribe(ctx, "", false)
}

// WatchFrom implements contracts.Resumable.
func (p *Provider) WatchFrom(ctx context.Context, lastRev string) (<-chan contracts.Event, error) {
	if lastRev == "" {
		return p.Watch(ctx)
	}
	return p.subscribe(ctx, lastRev, true)
}

// Dropped returns the number of messages dropped because the outbound
// event channel was full. Exported for tests / metrics.
func (p *Provider) Dropped() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dropped
}

func (p *Provider) subscribe(ctx context.Context, lastRev string, resumable bool) (<-chan contracts.Event, error) {
	out := make(chan contracts.Event, 16)
	handler := func(msg Msg) {
		m, err := p.codec.Decode(msg.Data)
		if err != nil {
			// Bad message — skip silently; bus pattern signals via separate hook.
			return
		}
		p.mu.Lock()
		p.last = m
		if msg.Revision != "" {
			p.lastRev = msg.Revision
		}
		p.lastAt = time.Now()
		p.hasFirst = true
		p.mu.Unlock()
		ev := contracts.Event{
			Source:   p.name,
			Reason:   "nats-push",
			Revision: msg.Revision,
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

	var (
		sub Subscription
		err error
	)
	if resumable {
		sub, err = p.conn.SubscribeFrom(p.subject, lastRev, handler)
	} else {
		sub, err = p.conn.Subscribe(p.subject, handler)
	}
	if err != nil {
		close(out)
		return nil, err
	}
	go func() {
		<-ctx.Done()
		_ = sub.Unsubscribe()
		close(out)
	}()
	return out, nil
}

// Compile-time interface assertions.
var (
	_ contracts.Provider         = (*Provider)(nil)
	_ contracts.SnapshotProvider = (*Provider)(nil)
	_ contracts.Resumable        = (*Provider)(nil)
)
