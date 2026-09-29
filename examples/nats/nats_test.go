package nats_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts/providertest"
	natsprov "github.com/fastabc/fastconf/examples/nats"
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
		return nil, errors.New("revision compacted")
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
	ch, err := p.Watch(ctx, "")
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
	snap, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Map["key"] != "v" {
		t.Errorf("load got %v", snap.Map)
	}
	if snap.Revision != "r1" || snap.Stale {
		t.Errorf("snapshot: %+v", snap)
	}
}

func TestProvider_LoadEmptyBeforeFirstMessage(t *testing.T) {
	c := newFakeConn(false)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	snap, err := p.Load(context.Background())
	if err != nil || len(snap.Map) != 0 {
		t.Errorf("expected empty map, got %v err=%v", snap.Map, err)
	}
	if !snap.Stale {
		t.Error("expected stale snapshot before first message")
	}
}

func TestProvider_WatchResumesFromRevision(t *testing.T) {
	c := newFakeConn(true)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := p.Watch(ctx, "last-rev-42")
	if err != nil {
		t.Fatalf("Watch: %v", err)
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

// A connection that cannot resume falls back to a cold subscribe; the first
// event reports the gap and later events do not.
func TestProvider_WatchResumeFailureMarksGap(t *testing.T) {
	c := newFakeConn(false)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := p.Watch(ctx, "rev")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	for i, wantGap := range []bool{true, false} {
		c.publish("s", natsprov.Msg{Data: []byte("a: 1"), Revision: fmt.Sprint("r", i)})
		select {
		case ev := <-ch:
			if ev.Gap != wantGap {
				t.Fatalf("event %d Gap = %v; want %v", i, ev.Gap, wantGap)
			}
		case <-ctx.Done():
			t.Fatalf("event %d not delivered", i)
		}
	}
}

func TestProvider_Conformance(t *testing.T) {
	c := newFakeConn(false)
	p, _ := natsprov.New("n", "s", kvCodec{}, c)
	providertest.AssertProviderBasics(t, p)
	providertest.AssertWatchClosesOnCancel(t, p, time.Second)
}
