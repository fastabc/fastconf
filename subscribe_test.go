package fastconf_test

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
)

// subCfg is the typed config used by the Subscribe test suite. Two
// independent sub-structs (DB, Server) let tests target one slice of
// config while leaving the other untouched.
type subCfg struct {
	DB     subCfgDB     `yaml:"db"`
	Server subCfgServer `yaml:"server"`
}

type subCfgDB struct {
	DSN  string `yaml:"dsn"`
	Pool int    `yaml:"pool"`
}

type subCfgServer struct {
	Addr string `yaml:"addr"`
}

func newSubMgr(t *testing.T, base string) *fastconf.Manager[subCfg] {
	t.Helper()
	mgr, err := fastconf.New[subCfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte(base)},
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

// Each reload checks both callback count and old/new values. Overrides are
// one-shot, so every step supplies the complete intended configuration.
func TestSubscribe_Changes(t *testing.T) {
	mgr := newSubMgr(t, "db: {dsn: v1, pool: 5}\nserver: {addr: ':8080'}\n")
	type pair struct{ old, next subCfgDB }
	var got []pair
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, next *subCfgDB) { got = append(got, pair{*old, *next}) },
	)
	defer cancel()

	for _, tc := range []struct {
		name, dsn, addr string
		want            []pair
	}{
		{"changed", "v2", ":8080", []pair{{subCfgDB{"v1", 5}, subCfgDB{"v2", 5}}}},
		{"unrelated change", "v2", ":9090", nil},
		{"identical reload", "v2", ":9090", nil},
		{"changed again", "v3", ":9090", []pair{{subCfgDB{"v2", 5}, subCfgDB{"v3", 5}}}},
	} {
		got = nil
		if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
			"db":     map[string]any{"dsn": tc.dsn, "pool": 5},
			"server": map[string]any{"addr": tc.addr},
		})); err != nil {
			t.Fatalf("%s: Reload: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: callbacks = %v; want %v", tc.name, got, tc.want)
		}
	}
}

// TestSubscribe_WithEqual_CustomComparator — caller-supplied equality
// overrides DeepEqual. Here we ignore the Pool field.
func TestSubscribe_WithEqual_CustomComparator(t *testing.T) {
	base := "db:\n  dsn: postgres://v1\n  pool: 5\n"
	mgr := newSubMgr(t, base)

	var fired atomic.Int32
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { fired.Add(1) },
		fastconf.WithEqual(func(a, b *subCfgDB) bool {
			// Treat as equal when DSN matches; Pool changes are ignored.
			return a.DSN == b.DSN
		}),
	)
	defer cancel()

	// Pool changes — equal returns true → callback skipped.
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v1", "pool": 99},
	})); err != nil {
		t.Fatal(err)
	}
	if got := fired.Load(); got != 0 {
		t.Errorf("after pool-only change: want 0, got %d", got)
	}

	// DSN changes — equal returns false → callback fires.
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v2", "pool": 99},
	})); err != nil {
		t.Fatal(err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("after DSN change: want 1, got %d", got)
	}
}

// WithEqual can force callbacks for unrelated commits, but cannot bypass hash dedupe.
func TestSubscribe_WithEqual_EveryCommit(t *testing.T) {
	base := "db:\n  dsn: postgres://same\n  pool: 5\nserver:\n  addr: :8080\n"
	mgr := newSubMgr(t, base)

	var fired atomic.Int32
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { fired.Add(1) },
		fastconf.WithEqual(func(_, _ *subCfgDB) bool { return false }),
	)
	defer cancel()

	// Change only server.addr; DB unchanged but equal always returns false.
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"server": map[string]any{"addr": ":9090"},
	})); err != nil {
		t.Fatal(err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("unrelated commit: expected 1 callback, got %d", got)
	}
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"server": map[string]any{"addr": ":9090"},
	})); err != nil {
		t.Fatal(err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("unchanged reload: expected no additional callback, got %d", got)
	}
}

// TestSubscribe_WithEqual_NotInvokedForNilTransition — nil ↔ non-nil
// transitions bypass equal entirely (per the API contract).
func TestSubscribe_WithEqual_NotInvokedForNilTransition(t *testing.T) {
	base := "db:\n  dsn: postgres://v1\n  pool: 5\n"
	mgr := newSubMgr(t, base)

	var equalCalls atomic.Int32
	var firedWithNilOld atomic.Bool

	// extract returns nil when DSN is empty (simulates a "missing slice"
	// configuration that flips between present and absent).
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB {
			if c.DB.DSN == "" {
				return nil
			}
			return &c.DB
		},
		func(old, new *subCfgDB) {
			if old == nil {
				firedWithNilOld.Store(true)
			}
		},
		fastconf.WithEqual(func(a, b *subCfgDB) bool {
			equalCalls.Add(1)
			return false
		}),
	)
	defer cancel()

	// Initial state has DSN=v1 (non-nil). Reload with empty DSN → extract
	// returns nil → non-nil → nil transition → equal MUST NOT be called.
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"db": map[string]any{"dsn": "", "pool": 0},
	})); err != nil {
		t.Fatal(err)
	}
	if c := equalCalls.Load(); c != 0 {
		t.Errorf("equal must not be invoked on nil transition, got %d calls", c)
	}

	// Reload back to non-nil → nil → non-nil transition → still no equal call.
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v2", "pool": 5},
	})); err != nil {
		t.Fatal(err)
	}
	if c := equalCalls.Load(); c != 0 {
		t.Errorf("equal must not be invoked on nil transition, got %d calls", c)
	}
	if !firedWithNilOld.Load() {
		t.Errorf("nil → non-nil transition should fire callback with old == nil")
	}
}

// TestSubscribe_PanicInEqualIsRecovered — a panic from a WithEqual
// comparator must not crash the writer; it is recovered like a panic
// from fn itself and surfaced on the Errors channel.
func TestSubscribe_PanicInEqualIsRecovered(t *testing.T) {
	base := "db:\n  dsn: postgres://v1\n  pool: 5\n"
	mgr := newSubMgr(t, base)

	var goodCalls atomic.Int32
	cancelBad := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { /* unreachable when equal panics */ },
		fastconf.WithEqual(func(a, b *subCfgDB) bool { panic("equal-boom") }),
	)
	defer cancelBad()
	cancelGood := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { goodCalls.Add(1) },
	)
	defer cancelGood()

	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v2", "pool": 5},
	})); err != nil {
		t.Fatalf("Reload must not propagate subscriber panic: %v", err)
	}

	// The good subscriber observes the DSN change; the bad one's panic is
	// isolated.
	if got := goodCalls.Load(); got != 1 {
		t.Errorf("good subscriber: want 1, got %d", got)
	}

	// The panic surfaces on Errors() — drain non-blockingly.
	select {
	case re := <-mgr.Errors():
		if re.Reason == "" || re.Err == nil {
			t.Errorf("expected non-empty reason and err on subscriber panic, got %+v", re)
		}
	case <-time.After(200 * time.Millisecond):
		t.Errorf("expected subscriber panic to surface on Errors() within 200ms")
	}
}

// drain pulls up to n events off ch within timeout; returns however many
// it managed to collect.
func drain(t *testing.T, ch <-chan fastconf.ReloadError, n int, timeout time.Duration) []fastconf.ReloadError {
	t.Helper()
	out := make([]fastconf.ReloadError, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(out) < n {
		select {
		case re, ok := <-ch:
			if !ok {
				t.Fatalf("Errors channel closed after %d of %d events", len(out), n)
			}
			out = append(out, re)
		case <-deadline.C:
			return out
		}
	}
	return out
}

func TestSubscribe_RegistrationOrder(t *testing.T) {
	mgr := newSubMgr(t, "db: {dsn: v1, pool: 5}\n")
	var got []int
	var cancels []func()
	for i := range 16 {
		cancels = append(cancels, fastconf.Subscribe(mgr,
			func(c *subCfg) *subCfgDB { return &c.DB },
			func(_, _ *subCfgDB) { got = append(got, i) }))
	}
	cancels[3]()
	want := []int{0, 1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	for pool := 6; pool < 10; pool++ {
		got = nil
		if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
			"db": map[string]any{"pool": pool},
		})); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("callbacks = %v, want %v", got, want)
		}
	}
}
