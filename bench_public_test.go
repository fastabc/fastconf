package fastconf_test

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

func BenchmarkGet(b *testing.B) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = mgr.Get().Database.Pool
	}
}

func BenchmarkGetParallel(b *testing.B) {
	mfs := newFS(nil)
	mgr, err := fastconf.New[appCfg](context.Background(), fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
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
