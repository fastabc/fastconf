package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
	httpprov "github.com/fastabc/fastconf/providers/http"
)

func TestWatchThenReloadUsesAcceptedCache(t *testing.T) {
	for _, etags := range []bool{false, true} {
		t.Run(fmt.Sprint("etag=", etags), func(t *testing.T) {
			type document struct{ body, tag string }
			var current atomic.Pointer[document]
			var notModified atomic.Int32
			current.Store(&document{`{"value":1}`, `"1"`})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				d := current.Load()
				if etags {
					w.Header().Set("ETag", d.tag)
					if r.Header.Get("If-None-Match") == d.tag {
						notModified.Add(1)
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
				_, _ = fmt.Fprint(w, d.body)
			}))
			defer srv.Close()
			c, _ := codec.Lookup("json")
			p, err := httpprov.New("remote", srv.URL, c, httpprov.WithInterval(time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			type config struct {
				Value int `json:"value"`
			}
			m, err := fastconf.New[config](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.json": &fstest.MapFile{Data: []byte(`{}`)}}), fastconf.WithProvider(p))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			// This test drives reloads from its own Watch subscription. The
			// manager's provider watcher would otherwise reload first and
			// accept each change before this subscription can observe it.
			m.Pause()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			events, err := p.Watch(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			wait := func() {
				t.Helper()
				select {
				case <-events:
				case <-ctx.Done():
					t.Fatal("no change event")
				}
			}
			current.Store(&document{`{"value":2}`, `"2"`})
			wait()
			if err := m.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if m.Get().Value != 2 {
				t.Fatal("watch advanced cache without publishing value")
			}
			before := m.Snapshot()
			current.Store(&document{`broken`, `"3"`})
			wait()
			if err := m.Reload(ctx); err == nil {
				t.Fatal("invalid document accepted")
			}
			if m.Snapshot() != before {
				t.Fatal("failed reload published")
			}
			// Correcting the body under the rejected ETag must cause a new read.
			current.Store(&document{`{"value":3}`, `"3"`})
			if err := m.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if m.Get().Value != 3 {
				t.Fatal("failed decode advanced accepted ETag")
			}
			current.Store(&document{`{"value":4}`, `"4"`})
			wait()
			current.Store(&document{`{"value":5}`, `"5"`})
			if err := m.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if m.Get().Value != 5 {
				t.Fatal("reload missed newest value")
			}
			before = m.Snapshot()
			if err := m.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if m.Snapshot() != before {
				t.Fatal("no-op published")
			}
			cancel()
			for range events {
			} // Wait for this watcher to stop before counting reads.
			current.Store(&document{`{"value":5}`, `"6"`})
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			count := notModified.Load()
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			// The paused manager watcher still polls p, so extra 304s may land
			// here. If the new ETag were not accepted no request could get a
			// 304, so "at least one more" still pins acceptance.
			if etags && notModified.Load() <= count {
				t.Fatal("same-body response did not accept new ETag")
			}
			if m.Snapshot() != before {
				t.Fatal("revision-only change published new value")
			}
		})
	}
}
