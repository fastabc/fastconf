package fastconf

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
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

func BenchmarkGetInternalNoState(b *testing.B) {
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

func BenchmarkReload(b *testing.B) {
	benchmarkReloadNoop(b)
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
		state := &State[benchCfg]{Value: &cfg}
		benchSettingsSink = state.Introspect().Settings()
	}
}

func BenchmarkIntrospectWarmKeys(b *testing.B) {
	cfg := benchCfg{A: 1, B: "hello"}
	cfg.C.D = true
	cfg.C.E = "world"
	state := &State[benchCfg]{Value: &cfg}
	intro := state.Introspect()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchKeysSink = intro.Keys()
	}
}

func BenchmarkExplainDeep(b *testing.B) {
	idx := newOriginIndex(ProvenanceFull)
	path := strings.Repeat("node.", 31) + "leaf"
	for i := 0; i < 16; i++ {
		idx.record(path, SourceRef{Path: fmt.Sprintf("layer-%02d", i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchOriginsSink = idx.Explain(path)
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
