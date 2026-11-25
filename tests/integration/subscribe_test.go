package integration_test

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

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

func TestSubscribe_WithEqual_NotInvokedForNilTransition(t *testing.T) {
	mgr := newSubMgr(t, "db: {dsn: v1}")
	type transition struct{ oldNil, newNil bool }
	var got []transition
	cancel := fastconf.Subscribe(mgr,
		func(c *subCfg) *subCfgDB {
			if c.DB.DSN == "" {
				return nil
			}
			return &c.DB
		},
		func(old, next *subCfgDB) { got = append(got, transition{old == nil, next == nil}) },
		fastconf.WithEqual(func(_, _ *subCfgDB) bool {
			t.Error("comparator called on a nil transition")
			return false
		}),
	)
	defer cancel()

	for _, dsn := range []string{"", "v2"} {
		if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
			"db": map[string]any{"dsn": dsn},
		})); err != nil {
			t.Fatal(err)
		}
	}
	want := []transition{{newNil: true}, {oldNil: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nil transitions = %+v, want %+v", got, want)
	}
}

func TestSubscribe_PanicIsolated(t *testing.T) {
	extract := func(c *subCfg) *string { return &c.DB.DSN }
	for _, tc := range []struct {
		name string
		opts []fastconf.SubscribeOption[string]
	}{
		{"callback", nil},
		{"comparator", []fastconf.SubscribeOption[string]{fastconf.WithEqual(func(_, _ *string) bool { panic("comparator") })}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newSubMgr(t, "db: {dsn: v1}")
			fastconf.Subscribe(mgr, extract, func(_, _ *string) { panic("callback") }, tc.opts...)
			goodCalls := 0
			fastconf.Subscribe(mgr, extract, func(_, _ *string) { goodCalls++ })

			if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
				"db": map[string]any{"dsn": "v2"},
			})); err != nil {
				t.Fatal(err)
			}
			if goodCalls != 1 {
				t.Errorf("later subscriber called %d times, want 1", goodCalls)
			}
			re := await(t, mgr.Errors())
			if re.Reason != "subscriber-panic" || re.Err == nil || !strings.Contains(re.Err.Error(), tc.name) {
				t.Errorf("reported error = %+v, want subscriber-panic from %s", re, tc.name)
			}
		})
	}
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

func TestSubscribe_CancelStops(t *testing.T) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	var calls atomic.Int64
	cancel := fastconf.Subscribe(mgr, func(c *appCfg) *string { return &c.Server.Addr }, func(_, _ *string) { calls.Add(1) })

	mfs["conf.d/base/00-app.yaml"] = &fstest.MapFile{Data: []byte("server:\n  addr: \":9001\"\nfeatures: [a, b]\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	mfs["conf.d/base/00-app.yaml"] = &fstest.MapFile{Data: []byte("server:\n  addr: \":9002\"\nfeatures: [a, b]\n")}
	if err := mgr.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("after cancel: want 1, got %d", got)
	}
}
