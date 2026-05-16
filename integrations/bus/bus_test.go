package bus

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

type cfg struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func TestMemoryBroker_PubSub(t *testing.T) {
	b := NewMemoryBroker(4)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := b.Subscribe(ctx, "cfg.app")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, Message{Subject: "cfg.app", Payload: []byte("name: x"), Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-ch:
		if m.Revision != "r1" || string(m.Payload) != "name: x" {
			t.Fatalf("got %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting message")
	}
}

func TestBusProvider_WithManager(t *testing.T) {
	b := NewMemoryBroker(4)
	defer b.Close()
	bp := New("bus", "cfg.app", 8500, b, nil)

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: base\nport: 1\n")},
	}
	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"),
		fastconf.WithProvider(bp),
		fastconf.WithWatch(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if mgr.Get().Name != "base" {
		t.Fatalf("initial name = %q", mgr.Get().Name)
	}

	// Publish a bus update; manager should converge.
	deadline := time.Now().Add(2 * time.Second)
	for tries := 0; ; tries++ {
		_ = b.Publish(context.Background(), Message{
			Subject: "cfg.app", Payload: []byte("port: 8080"), Revision: "r1",
		})
		time.Sleep(50 * time.Millisecond)
		if mgr.Get().Port == 8080 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("port did not converge, got %+v after %d tries", mgr.Get(), tries)
		}
	}
}
