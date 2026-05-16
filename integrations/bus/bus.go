// Package bus provides a small message-bus abstraction for FastConf.
//
// Many infrastructure teams already standardise on a publish/subscribe
// system (NATS, Kafka, Redis pub/sub, MQTT, GCP Pub/Sub, ...). When a
// configuration change is broadcast over such a bus, every subscribing
// instance can refresh in milliseconds without polling. This package
// exposes a Broker interface that hides the chosen transport and a
// BusProvider that adapts any Broker into a contracts.Provider so it
// can participate in a Manager's normal merge pipeline.
//
// The MemoryBroker reference implementation is goroutine-safe and
// production-suitable for single-process tests / examples; real
// transports (NATS, Kafka, ...) live in sibling sub-modules so users
// only pay for the dependency they actually use.
package bus

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"gopkg.in/yaml.v3"
)

// Message is a single payload broadcast through the bus.
type Message struct {
	// Subject is the topic / channel name the message was published on.
	Subject string
	// Payload carries the raw bytes (typically YAML or JSON).
	Payload []byte
	// Revision is an optional opaque version identifier; when non-empty
	// it is forwarded as Event.Revision and Snapshot.Revision so the
	// reload pipeline can short-circuit duplicate broadcasts.
	Revision string
	// At is the publish timestamp (set by the broker).
	At time.Time
}

// Broker is a minimal pub/sub abstraction. Implementations MUST be safe
// for concurrent use. Subscribe returns a channel that is closed when
// ctx is cancelled or the broker is closed.
type Broker interface {
	Publish(ctx context.Context, msg Message) error
	Subscribe(ctx context.Context, subject string) (<-chan Message, error)
	Close() error
}

// MemoryBroker is an in-process Broker reference. It keeps a fan-out
// list of subscriber channels per subject and never blocks the publisher
// (slow subscribers drop the oldest message — back-pressure is the
// caller's responsibility).
type MemoryBroker struct {
	mu      sync.RWMutex
	subs    map[string][]chan Message
	closed  bool
	bufSize int
}

// NewMemoryBroker returns a MemoryBroker whose per-subscriber buffer
// holds bufSize messages (default 16).
func NewMemoryBroker(bufSize int) *MemoryBroker {
	if bufSize <= 0 {
		bufSize = 16
	}
	return &MemoryBroker{subs: map[string][]chan Message{}, bufSize: bufSize}
}

// Publish delivers msg to every active subscriber of msg.Subject.
func (b *MemoryBroker) Publish(_ context.Context, msg Message) error {
	if msg.At.IsZero() {
		msg.At = time.Now()
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return errors.New("bus: broker closed")
	}
	subs := append([]chan Message(nil), b.subs[msg.Subject]...)
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			// drop oldest, then push
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			default:
			}
		}
	}
	return nil
}

// Subscribe returns a channel for new messages on subject; closed when
// ctx is cancelled or Close() is called.
func (b *MemoryBroker) Subscribe(ctx context.Context, subject string) (<-chan Message, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("bus: broker closed")
	}
	ch := make(chan Message, b.bufSize)
	b.subs[subject] = append(b.subs[subject], ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[subject]
		for i, c := range list {
			if c == ch {
				b.subs[subject] = append(list[:i], list[i+1:]...)
				close(ch)
				return
			}
		}
	}()
	return ch, nil
}

// Close shuts the broker down; subsequent Publish/Subscribe calls error.
func (b *MemoryBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, list := range b.subs {
		for _, ch := range list {
			close(ch)
		}
	}
	b.subs = map[string][]chan Message{}
	return nil
}

// Decoder converts raw payload bytes into a generic map[string]any. The
// default decoder uses YAML which is also a JSON superset.
type Decoder func([]byte) (map[string]any, error)

// YAMLDecoder is the default Decoder.
func YAMLDecoder(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BusProvider adapts a Broker into a contracts.SnapshotProvider.
//
// On Watch, it subscribes to Subject and forwards every message as a
// contracts.Event whose Revision matches Message.Revision; the latest
// Message is cached so Load() / LoadSnapshot() return the most recent
// payload (or an empty map until the first message arrives).
type BusProvider struct {
	name    string
	prio    int
	subject string
	broker  Broker
	decoder Decoder

	mu       sync.RWMutex
	last     map[string]any
	lastRev  string
	lastAt   time.Time
	hasFirst bool

	// onDecodeError, when non-nil, is invoked for every payload the
	// Decoder rejects. Phase 20 BUG-207: surface malformed bus
	// messages instead of silently dropping them.
	onDecodeError func(subject string, payload []byte, err error)
}

// SetOnDecodeError installs a callback fired on each Decoder failure.
// Pass nil to detach. Safe to call before or after Watch.
func (p *BusProvider) SetOnDecodeError(fn func(subject string, payload []byte, err error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onDecodeError = fn
}

// New returns a BusProvider over broker for the given subject. Use
// nil decoder to default to YAML.
func New(name, subject string, prio int, broker Broker, dec Decoder) *BusProvider {
	if dec == nil {
		dec = YAMLDecoder
	}
	return &BusProvider{
		name:    name,
		prio:    prio,
		subject: subject,
		broker:  broker,
		decoder: dec,
		last:    map[string]any{},
	}
}

// Name implements contracts.Provider.
func (p *BusProvider) Name() string { return p.name }

// Priority implements contracts.Provider.
func (p *BusProvider) Priority() int { return p.prio }

// Load returns the most recently observed payload (empty map until a
// message arrives).
func (p *BusProvider) Load(_ context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]any, len(p.last))
	for k, v := range p.last {
		out[k] = v
	}
	return out, nil
}

// LoadSnapshot implements contracts.SnapshotProvider, exposing the
// revision and a Stale flag (Stale=true until the first message arrives).
func (p *BusProvider) LoadSnapshot(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.Load(ctx)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return contracts.Snapshot{Map: m, Revision: p.lastRev, Stale: !p.hasFirst}, nil
}

// Watch subscribes to the bus subject and forwards events.
func (p *BusProvider) Watch(ctx context.Context) (<-chan contracts.Event, error) {
	in, err := p.broker.Subscribe(ctx, p.subject)
	if err != nil {
		return nil, err
	}
	out := make(chan contracts.Event, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-in:
				if !ok {
					return
				}
				m, derr := p.decoder(msg.Payload)
				if derr != nil {
					p.mu.RLock()
					cb := p.onDecodeError
					p.mu.RUnlock()
					if cb != nil {
						cb(p.subject, msg.Payload, derr)
					}
					continue
				}
				p.mu.Lock()
				p.last = m
				p.lastRev = msg.Revision
				p.lastAt = msg.At
				p.hasFirst = true
				p.mu.Unlock()
				ev := contracts.Event{
					Source:   p.name,
					Reason:   "bus",
					Revision: msg.Revision,
					At:       msg.At,
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// Compile-time interface assertions.
var (
	_ contracts.Provider         = (*BusProvider)(nil)
	_ contracts.SnapshotProvider = (*BusProvider)(nil)
)
