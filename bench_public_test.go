package fastconf_test

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
)

func BenchmarkGet(b *testing.B) {
	mgr := newPublicBenchmarkManager(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = mgr.Get().Database.Pool
	}
}

func BenchmarkGetParallel(b *testing.B) {
	mgr := newPublicBenchmarkManager(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var local int
		for pb.Next() {
			local = mgr.Get().Database.Pool
		}
		runtime.KeepAlive(local)
	})
}

// BenchmarkReloadLarge exercises the full reload pipeline against a
// synthetic 256-key configuration. It is intended as a regression
// guard for assemble + merge + decode allocations.
func BenchmarkReloadLarge(b *testing.B) {
	const n = 256
	mfs := fstest.MapFS{}
	for i := 0; i < n; i++ {
		mfs[fmt.Sprintf("conf.d/base/%03d.yaml", i)] = &fstest.MapFile{
			Data: []byte(fmt.Sprintf("k%03d: %d\n", i, i)),
		}
	}
	mgr, err := fastconf.New[map[string]any](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReloadLargeIncremental changes one of 256 YAML layers per reload.
// File replacement is included; every iteration must publish the new value.
func BenchmarkReloadLargeIncremental(b *testing.B) {
	const n = 256
	mfs := fstest.MapFS{}
	for i := 0; i < n; i++ {
		mfs[fmt.Sprintf("conf.d/base/%03d.yaml", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("k%03d: %d\n", i, i))}
	}
	mgr, err := fastconf.New[map[string]any](context.Background(), fastconf.WithFS(mfs))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	generation := mgr.Snapshot().Generation()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mfs["conf.d/base/000.yaml"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("k000: %d\n", i+1))}
		if err := mgr.Reload(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if mgr.Snapshot().Generation() != generation+uint64(b.N) {
		b.Fatal("incremental reload did not publish every change")
	}
	if got := (*mgr.Get())["k000"]; got != json.Number(fmt.Sprint(b.N)) {
		b.Fatalf("last value = %v, want %d", got, b.N)
	}
}

// sinkInt prevents the compiler from optimising away the Get() call and
// gives the BenchmarkGet allocs report a stable consumer of the value.
var sinkInt int

type publicBenchmarkConfig struct {
	Database struct {
		Pool int `json:"pool"`
	} `json:"database"`
}

func newPublicBenchmarkManager(b testing.TB) *fastconf.Manager[publicBenchmarkConfig] {
	b.Helper()
	m, err := fastconf.New[publicBenchmarkConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("database: {pool: 10}\n")}}))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = m.Close() })
	return m
}

type wideBenchmarkProvider struct{ data map[string]any }

func (*wideBenchmarkProvider) Name() string { return "wide-benchmark" }
func (p *wideBenchmarkProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: p.data}, nil
}

// BenchmarkReloadWide forces the full pipeline through a provider on every reload,
// rather than taking the unchanged-file input fingerprint shortcut.
func BenchmarkReloadWide(b *testing.B) {
	sections := map[string]any{}
	for i := range 100 {
		section := map[string]any{}
		for j := range 100 {
			section[fmt.Sprintf("key%03d", j)] = j
		}
		sections[fmt.Sprintf("section%03d", i)] = section
	}
	type C struct {
		Sections map[string]map[string]int `json:"sections"`
	}
	m, err := fastconf.New[C](context.Background(), fastconf.WithProvider(&wideBenchmarkProvider{map[string]any{"sections": sections}}))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := m.Reload(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
