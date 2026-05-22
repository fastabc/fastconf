package fastconf_test

import (
	"context"
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
		fastconf.PresetTesting(fastconf.TestingOpts{
			FS: fstest.MapFS{
				"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte(base)},
			},
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

// TestSubscribe_FiresOnChange — default behavior: callback fires when the
// extracted sub-struct actually changes between two reloads.
func TestSubscribe_FiresOnChange(t *testing.T) {
	base := "db:\n  dsn: postgres://old\n  pool: 5\nserver:\n  addr: :8080\n"
	mgr := newSubMgr(t, base)

	var fired atomic.Int32
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { fired.Add(1) },
	)
	defer cancel()

	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://new", "pool": 5},
	})); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("expected 1 callback, got %d", got)
	}
}

// TestSubscribe_SkipsWhenUnchanged — default: only the changed field's
// subscriber fires; the unrelated subscriber stays silent.
func TestSubscribe_SkipsWhenUnchanged(t *testing.T) {
	base := "db:\n  dsn: postgres://same\n  pool: 5\nserver:\n  addr: :8080\n"
	mgr := newSubMgr(t, base)

	var fired atomic.Int32
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { fired.Add(1) },
	)
	defer cancel()

	// Change only server.addr — DB is untouched.
	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"server": map[string]any{"addr": ":9090"},
	})); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := fired.Load(); got != 0 {
		t.Errorf("expected 0 callbacks (DB unchanged), got %d", got)
	}
}

// TestSubscribe_MultipleReloads — across a sequence of reloads, fn fires
// only when the relevant field actually changes.
func TestSubscribe_MultipleReloads(t *testing.T) {
	base := "db:\n  dsn: postgres://v1\n  pool: 5\nserver:\n  addr: :8080\n"
	mgr := newSubMgr(t, base)

	var fired atomic.Int32
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) { fired.Add(1) },
	)
	defer cancel()

	// reload 1: DB changes v1 → v2 → fires
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v2", "pool": 5},
	}))
	// reload 2: DB stays at v2, only server changes → does NOT fire
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db":     map[string]any{"dsn": "postgres://v2", "pool": 5},
		"server": map[string]any{"addr": ":9090"},
	}))
	// reload 3: DB changes v2 → v3 → fires
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db":     map[string]any{"dsn": "postgres://v3", "pool": 5},
		"server": map[string]any{"addr": ":9090"},
	}))

	if got := fired.Load(); got != 2 {
		t.Errorf("expected 2 callbacks, got %d", got)
	}
}

// TestSubscribe_ReceivesCorrectValues — old and new carry the expected
// boundary values across a change.
func TestSubscribe_ReceivesCorrectValues(t *testing.T) {
	base := "db:\n  dsn: postgres://before\n  pool: 5\n"
	mgr := newSubMgr(t, base)

	type pair struct{ old, new string }
	var got pair
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB { return &c.DB },
		func(old, new *subCfgDB) {
			if old != nil {
				got.old = old.DSN
			}
			if new != nil {
				got.new = new.DSN
			}
		},
	)
	defer cancel()

	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://after", "pool": 5},
	}))

	if got.old != "postgres://before" {
		t.Errorf("old DSN: want postgres://before, got %q", got.old)
	}
	if got.new != "postgres://after" {
		t.Errorf("new DSN: want postgres://after, got %q", got.new)
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
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v1", "pool": 99},
	}))
	if got := fired.Load(); got != 0 {
		t.Errorf("after pool-only change: want 0, got %d", got)
	}

	// DSN changes — equal returns false → callback fires.
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v2", "pool": 99},
	}))
	if got := fired.Load(); got != 1 {
		t.Errorf("after DSN change: want 1, got %d", got)
	}
}

// TestSubscribe_WithEqual_FireAlwaysIdiom — the documented escape hatch
// for the v0.18 "fire on every reload" semantics: WithEqual returning
// false unconditionally.
func TestSubscribe_WithEqual_FireAlwaysIdiom(t *testing.T) {
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
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"server": map[string]any{"addr": ":9090"},
	}))
	if got := fired.Load(); got != 1 {
		t.Errorf("fire-always idiom: expected 1 callback, got %d", got)
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
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "", "pool": 0},
	}))
	if c := equalCalls.Load(); c != 0 {
		t.Errorf("equal must not be invoked on nil transition, got %d calls", c)
	}

	// Reload back to non-nil → nil → non-nil transition → still no equal call.
	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"db": map[string]any{"dsn": "postgres://v2", "pool": 5},
	}))
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

	if err := mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
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
