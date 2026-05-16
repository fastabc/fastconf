package nats_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts"
	natsprov "github.com/fastabc/fastconf/providers/nats"
)

// fakeConn is a minimal in-memory implementation of nats.Conn used by
// the tests. A real wiring adapter looks identical — three forwarders
// over *nats.Conn from github.com/nats-io/nats.go.
type fakeConn struct {
	mu       sync.Mutex
	next     atomic.Uint64
	handlers map[string]map[uint64]func(natsprov.Msg)
	resumeOK bool
}

func newFakeConn(resumeOK bool) *fakeConn {
	return &fakeConn{handlers: map[string]map[uint64]func(natsprov.Msg){}, resumeOK: resumeOK}
}

type fakeSub struct {
	conn    *fakeConn
	subject string
	id      uint64
}

func (s *fakeSub) Unsubscribe() error {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()
	delete(s.conn.handlers[s.subject], s.id)
	return nil
}

func (c *fakeConn) Subscribe(subject string, handler func(natsprov.Msg)) (natsprov.Subscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handlers[subject] == nil {
		c.handlers[subject] = map[uint64]func(natsprov.Msg){}
	}
	id := c.next.Add(1)
	c.handlers[subject][id] = handler
	return &fakeSub{conn: c, subject: subject, id: id}, nil
}

func (c *fakeConn) SubscribeFrom(subject, _ string, handler func(natsprov.Msg)) (natsprov.Subscription, error) {
	if !c.resumeOK {
		return nil, contracts.ErrResumeUnsupported
	}
	return c.Subscribe(subject, handler)
}

func (c *fakeConn) publish(subject string, m natsprov.Msg) {
	c.mu.Lock()
	hs := make([]func(natsprov.Msg), 0, len(c.handlers[subject]))
	for _, h := range c.handlers[subject] {
		hs = append(hs, h)
	}
	c.mu.Unlock()
	for _, h := range hs {
		h(m)
	}
}

// trivial line-based codec for tests
type kvCodec struct{}

func (kvCodec) Decode(b []byte) (map[string]any, error) {
	out := map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			return nil, errors.New("bad line: " + line)
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	return out, nil
}

func TestNew_Validation(t *testing.T) {
	c := newFakeConn(false)
	if _, err := natsprov.New("", "s", kvCodec{}, c); err == nil {
		t.Error("expected error on empty name")
	}
	if _, err := natsprov.New("n", "", kvCodec{}, c); err == nil {
		t.Error("expected error on empty subject")
	}
	if _, err := natsprov.New("n", "s", nil, c); err == nil {
		t.Error("expected error on nil codec")
	}
	if _, err := natsprov.New("n", "s", kvCodec{}, nil); err == nil {
		t.Error("expected error on nil conn")
	}
}

func TestProvider_WatchPushesEvent(t *testing.T) {
	c := newFakeConn(false)
	p, err := natsprov.New("nats", "cfg.app", kvCodec{}, c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := p.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		c.publish("cfg.app", natsprov.Msg{Subject: "cfg.app", Data: []byte("key: v"), Revision: "r1"})
	}()
	select {
	case ev := <-ch:
		if ev.Source != "nats" || ev.Revision != "r1" {
			t.Errorf("event: %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("no event received")
	}
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["key"] != "v" {
		t.Errorf("load got %v", got)
	}
	snap, err := p.LoadSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Revision != "r1" || snap.Stale {
		t.Errorf("snapshot: %+v", snap)
	}
}

func TestProvider_LoadEmptyBeforeFirstMessage(t *testing.T) {
	c := newFakeConn(false)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	m, err := p.Load(context.Background())
	if err != nil || len(m) != 0 {
		t.Errorf("expected empty map, got %v err=%v", m, err)
	}
	snap, _ := p.LoadSnapshot(context.Background())
	if !snap.Stale {
		t.Error("expected stale snapshot before first message")
	}
}

func TestProvider_WatchFromResumes(t *testing.T) {
	c := newFakeConn(true)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := p.WatchFrom(ctx, "last-rev-42")
	if err != nil {
		t.Fatalf("WatchFrom: %v", err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		c.publish("s", natsprov.Msg{Data: []byte("a: 1"), Revision: "r99"})
	}()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("no event from resumed subscribe")
	}
}

func TestProvider_WatchFromUnsupportedFallback(t *testing.T) {
	c := newFakeConn(false)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	_, err := p.WatchFrom(context.Background(), "rev")
	if !errors.Is(err, contracts.ErrResumeUnsupported) {
		t.Errorf("expected ErrResumeUnsupported, got %v", err)
	}
}

func TestProvider_ImplementsContracts(t *testing.T) {
	c := newFakeConn(false)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	var _ contracts.Provider = p
	var _ contracts.SnapshotProvider = p
	var _ contracts.Resumable = p
}
