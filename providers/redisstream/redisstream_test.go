package redisstream_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastabc/fastconf/contracts"
	rsprov "github.com/fastabc/fastconf/providers/redisstream"
)

// fakeClient is an in-memory Redis-Streams stand-in for tests. A real
// wiring adapter looks like:
//
//	type rdb struct{ *redis.Client }
//	func (r rdb) XRead(ctx context.Context, stream, lastID string, block time.Duration) ([]rsprov.Entry, error) { ... }
type fakeClient struct {
	mu      sync.Mutex
	cond    *sync.Cond
	entries map[string][]rsprov.Entry
}

func newFakeClient() *fakeClient {
	c := &fakeClient{entries: map[string][]rsprov.Entry{}}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *fakeClient) Append(stream string, e rsprov.Entry) {
	c.mu.Lock()
	c.entries[stream] = append(c.entries[stream], e)
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *fakeClient) XRead(ctx context.Context, stream, lastID string, block time.Duration) ([]rsprov.Entry, error) {
	deadline := time.Now().Add(block)
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		// Find entries newer than lastID.
		next := []rsprov.Entry{}
		for _, e := range c.entries[stream] {
			if lastID == "$" {
				continue // tail: only entries added after subscribe
			}
			if e.ID > lastID {
				next = append(next, e)
			}
		}
		if len(next) > 0 {
			return next, nil
		}
		// First-time tail subscribe — promote lastID to current head once.
		if lastID == "$" {
			// Treat first wait under "$" as ready to accept new entries; we
			// detect "new" by snapshot length before/after wait.
		}
		preLen := len(c.entries[stream])
		// wait until either deadline or new append
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				c.cond.L.Lock()
				c.cond.Broadcast()
				c.cond.L.Unlock()
			case <-time.After(time.Until(deadline)):
				c.cond.L.Lock()
				c.cond.Broadcast()
				c.cond.L.Unlock()
			case <-done:
			}
		}()
		c.cond.Wait()
		close(done)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		if lastID == "$" {
			// Anything appended while we waited counts.
			if len(c.entries[stream]) > preLen {
				return append([]rsprov.Entry(nil), c.entries[stream][preLen:]...), nil
			}
		}
	}
}

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
	c := newFakeClient()
	if _, err := rsprov.New("", "s", kvCodec{}, c); err == nil {
		t.Error("expected error on empty name")
	}
	if _, err := rsprov.New("n", "", kvCodec{}, c); err == nil {
		t.Error("expected error on empty stream")
	}
	if _, err := rsprov.New("n", "s", nil, c); err == nil {
		t.Error("expected error on nil codec")
	}
	if _, err := rsprov.New("n", "s", kvCodec{}, nil); err == nil {
		t.Error("expected error on nil client")
	}
}

func TestProvider_WatchEmitsOnEntry(t *testing.T) {
	c := newFakeClient()
	p, err := rsprov.New("rs", "cfg:app", kvCodec{}, c, rsprov.WithBlock(200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := p.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		c.Append("cfg:app", rsprov.Entry{ID: "1-0", Fields: map[string]string{"payload": "k: v"}})
	}()
	select {
	case ev := <-ch:
		if ev.Source != "rs" || ev.Revision != "1-0" {
			t.Errorf("event: %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("no event received")
	}
	got, _ := p.Load(context.Background())
	if got["k"] != "v" {
		t.Errorf("load got %v", got)
	}
	snap, _ := p.LoadSnapshot(context.Background())
	if snap.Stale || snap.Revision != "1-0" {
		t.Errorf("snapshot: %+v", snap)
	}
}

func TestProvider_WatchFromResumes(t *testing.T) {
	c := newFakeClient()
	// Seed prior entries.
	c.Append("s", rsprov.Entry{ID: "1-0", Fields: map[string]string{"payload": "a: 1"}})
	c.Append("s", rsprov.Entry{ID: "2-0", Fields: map[string]string{"payload": "b: 2"}})
	p, _ := rsprov.New("n", "s", kvCodec{}, c, rsprov.WithBlock(200*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := p.WatchFrom(ctx, "1-0")
	if err != nil {
		t.Fatal(err)
	}
	// Should immediately deliver entry 2-0.
	select {
	case ev := <-ch:
		if ev.Revision != "2-0" {
			t.Errorf("expected 2-0, got %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("no resumed event")
	}
}

func TestProvider_ImplementsContracts(t *testing.T) {
	c := newFakeClient()
	p, _ := rsprov.New("n", "s", kvCodec{}, c)
	var _ contracts.Provider = p
	var _ contracts.SnapshotProvider = p
	var _ contracts.Resumable = p
}
