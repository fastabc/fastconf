package fastconf

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/internal/obs"
)

type benchCfg struct {
	A int    `yaml:"a"`
	B string `yaml:"b"`
	C struct {
		D bool   `yaml:"d"`
		E string `yaml:"e"`
	} `yaml:"c"`
}

func newBenchManager(b testing.TB) *Manager[benchCfg] {
	b.Helper()
	mgr, err := New[benchCfg](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\nb: hello\nc:\n  d: true\n  e: world\n")},
		}),
	)
	if err != nil {
		b.Fatal(err)
	}
	return mgr
}

// BenchmarkGetWarmState measures Get() against a manager whose State[T]
// has been populated by an initial reload — the canonical hot-path
// shape (single atomic.Pointer.Load + struct-pointer return).
func BenchmarkGetWarmState(b *testing.B) {
	mgr := newBenchManager(b)
	defer mgr.Close()
	b.ReportAllocs()
	b.ResetTimer()
	var sink *benchCfg
	for i := 0; i < b.N; i++ {
		sink = mgr.Get()
	}
	_ = sink
}

func BenchmarkReloadNoop(b *testing.B) {
	benchmarkReloadNoop(b)
}

func benchmarkReloadNoop(b *testing.B) {
	mgr := newBenchManager(b)
	defer mgr.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Reload(ctx)
	}
}

// BenchmarkReloadAllocs is the v0.18 alloc baseline for the
// reload-with-commit path. Pairs with BenchmarkReloadCommitSmall (which
// has the same fixture) and is consumed by tools/bench-guard.sh as the
// SPEC-C1 regression guard.
func BenchmarkReloadAllocs(b *testing.B) {
	mgr := newBenchManager(b)
	defer mgr.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Reload(ctx, WithSourceOverride(map[string]any{
			"a": 2 + i%2,
		}))
	}
}

func BenchmarkReloadCommitSmall(b *testing.B) {
	mgr := newBenchManager(b)
	defer mgr.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Reload(ctx, WithSourceOverride(map[string]any{
			"a": 2 + i%2, // alternate so every reload publishes a new State
		}))
	}
}

// BenchmarkSubscribeContention exercises the RWMutex path: 100 quiet
// subscribers (read side) compete with frequent Subscribe/cancel churn
// (write side) under continuous reload. Pre-SPEC-C2 the sync.Mutex
// serialised both sides; under sync.RWMutex the read path runs in
// parallel with itself, which this benchmark surfaces.
func BenchmarkSubscribeContention(b *testing.B) {
	const subscriberCount = 100
	mgr := newBenchManager(b)
	defer mgr.Close()
	var dummyA int
	for range subscriberCount {
		Subscribe(mgr,
			func(c *benchCfg) *int { return &c.A },
			func(_, next *int) {
				if next != nil {
					dummyA += *next
				}
			},
		)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// One churning Subscribe+cancel pair on every reload exercises
		// the write side under read-path contention.
		cancel := Subscribe(mgr,
			func(c *benchCfg) *int { return &c.A },
			func(_, _ *int) {},
		)
		_ = mgr.Reload(ctx, WithSourceOverride(map[string]any{
			"a": 2 + i%2,
		}))
		cancel()
	}
	benchIntSink = dummyA
}

func BenchmarkReloadManySubscribers(b *testing.B) {
	for _, n := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			mgr := newBenchManager(b)
			defer mgr.Close()
			var sink int
			cancels := make([]func(), 0, n)
			for range n {
				cancels = append(cancels, Subscribe(mgr,
					func(c *benchCfg) *int { return &c.A },
					func(_, next *int) {
						if next != nil {
							sink += *next
						}
					},
				))
			}
			defer func() {
				for _, cancel := range cancels {
					cancel()
				}
			}()
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = mgr.Reload(ctx, WithSourceOverride(map[string]any{
					"a": 2 + i%2,
				}))
			}
			benchIntSink = sink
		})
	}
}

type benchTypedHooksWideCfg struct {
	D00 time.Duration `json:"d00"`
	D01 time.Duration `json:"d01"`
	D02 time.Duration `json:"d02"`
	D03 time.Duration `json:"d03"`
	D04 time.Duration `json:"d04"`
	D05 time.Duration `json:"d05"`
	D06 time.Duration `json:"d06"`
	D07 time.Duration `json:"d07"`
	D08 time.Duration `json:"d08"`
	D09 time.Duration `json:"d09"`
	D10 time.Duration `json:"d10"`
	D11 time.Duration `json:"d11"`
	D12 time.Duration `json:"d12"`
	D13 time.Duration `json:"d13"`
	D14 time.Duration `json:"d14"`
	D15 time.Duration `json:"d15"`
}

func BenchmarkTypedHooksWide(b *testing.B) {
	var src strings.Builder
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&src, "d%02d: 30s\n", i)
	}
	mgr, err := New[benchTypedHooksWideCfg](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(src.String())},
		}),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer mgr.Close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mgr.Reload(ctx)
	}
}

var (
	benchIntSink      int
	benchSettingsSink map[string]any
	benchKeysSink     []string
	benchOriginsSink  []Origin
)

func BenchmarkIntrospectCold(b *testing.B) {
	cfg := benchCfg{A: 1, B: "hello"}
	cfg.C.D = true
	cfg.C.E = "world"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state := wrapState(istate.NewSnapshot(&cfg, [32]byte{}, 0, nil, 0, nil, istate.ReloadCause{}, nil, nil))
		benchSettingsSink = state.Introspect().Settings()
	}
}

func BenchmarkIntrospectWarmKeys(b *testing.B) {
	cfg := benchCfg{A: 1, B: "hello"}
	cfg.C.D = true
	cfg.C.E = "world"
	state := wrapState(istate.NewSnapshot(&cfg, [32]byte{}, 0, nil, 0, nil, istate.ReloadCause{}, nil, nil))
	intro := state.Introspect()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchKeysSink = intro.Keys()
	}
}

type benchSpan struct{}

func (benchSpan) End()                     {}
func (benchSpan) RecordError(error)        {}
func (benchSpan) SetAttribute(string, any) {}

func BenchmarkEnrichAttrs4(b *testing.B) {
	var sp benchSpan
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		obs.EnrichAttrs(&sp,
			contracts.Attr{Key: "a", Value: 1},
			contracts.Attr{Key: "b", Value: "x"},
			contracts.Attr{Key: "c", Value: true},
			contracts.Attr{Key: "d", Value: int64(42)})
	}
}
