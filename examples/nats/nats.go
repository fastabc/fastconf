// Package nats demonstrates a streaming provider using an injected NATS-like
// connection. Watch caches decoded documents and offers events without blocking.
// JetStream-style SubscribeFrom resumes revisions; a cold fallback marks a gap.
// This is a reference implementation; supply an adapter for the real client.
package nats

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
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

	// SubscribeFrom is the resumable variant. Implementations that cannot
	// honor lastRev return an error; the provider then subscribes cold and
	// reports the gap on the first event.
	SubscribeFrom(subject, lastRev string, handler func(Msg)) (Subscription, error)
}

// Codec decodes a payload to a generic map.
type Codec = contracts.Codec

// Provider implements contracts.Provider and contracts.Describer.
type Provider struct {
	name     string
	subject  string
	priority int
	codec    Codec
	conn     Conn

	latest  providerutil.Latest
	dropped atomic.Uint64
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
// the first message arrives) with the last message revision.
func (p *Provider) Load(_ context.Context) (contracts.Snapshot, error) {
	return p.latest.Snapshot(), nil
}

// Watch implements contracts.Provider. A non-empty from resumes through
// Conn.SubscribeFrom; when the connection cannot honor it, Watch falls
// back to a cold Subscribe and marks the first event with Gap.
func (p *Provider) Watch(ctx context.Context, from string) (<-chan contracts.Event, error) {
	if from == "" {
		return p.subscribe(ctx, "", false, false)
	}
	ch, err := p.subscribe(ctx, from, true, false)
	if err == nil {
		return ch, nil
	}
	return p.subscribe(ctx, "", false, true)
}

// Dropped returns the number of messages dropped because the outbound
// event channel was full. Exported for tests / metrics.
func (p *Provider) Dropped() uint64 {
	return p.dropped.Load()
}

// subscribe starts delivery; gap marks the first event of a cold
// subscription that replaced a failed resume.
func (p *Provider) subscribe(ctx context.Context, lastRev string, resumable, gap bool) (<-chan contracts.Event, error) {
	out := make(chan contracts.Event, 16)
	var pendingGap atomic.Bool
	pendingGap.Store(gap)
	handler := func(msg Msg) {
		m, err := p.codec.Decode(msg.Data)
		if err != nil {
			// Bad message — skip silently; bus pattern signals via separate hook.
			return
		}
		p.latest.Store(m, msg.Revision)
		ev := contracts.Event{
			Source:   p.name,
			Reason:   "nats-push",
			Revision: msg.Revision,
			Gap:      pendingGap.Swap(false),
			At:       time.Now(),
		}
		if !providerutil.Offer(out, ev) {
			p.dropped.Add(1)
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
	_ contracts.Provider  = (*Provider)(nil)
	_ contracts.Describer = (*Provider)(nil)
)
