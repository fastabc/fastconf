package fastconf

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// BenchmarkGetWarmState measures Get() against a manager whose State[T]
// has been populated by an initial reload — the canonical hot-path
// shape (single atomic.Pointer.Load + struct-pointer return).
func BenchmarkGetWarmState(b *testing.B) {
	mgr := newRuntimeManager(b)
	defer func() { _ = mgr.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	var sink *runtimeConfig
	for i := 0; i < b.N; i++ {
		sink = mgr.Get()
	}
	_ = sink
}

func BenchmarkReloadNoop(b *testing.B) {
	mgr := newRuntimeManager(b)
	defer func() { _ = mgr.Close() }()
	ctx := context.Background()
	generation := mgr.Snapshot().Generation()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(ctx); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if mgr.Snapshot().Generation() != generation {
		b.Fatal("no-op reload published a state")
	}
}

func BenchmarkReloadCommitSmall(b *testing.B) {
	mgr := newRuntimeManager(b)
	defer func() { _ = mgr.Close() }()
	ctx := context.Background()
	generation := mgr.Snapshot().Generation()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(ctx, WithOverride(map[string]any{
			"a": 2 + i%2, // alternate so every reload publishes a new State
		})); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if got := mgr.Snapshot().Generation(); got != generation+uint64(b.N) {
		b.Fatalf("committed %d generations, want %d", got-generation, b.N)
	}
}

func BenchmarkReloadCommitYAML(b *testing.B) {
	mgr, err := New[struct {
		Value int `yaml:"value" json:"value"`
	}](context.Background(), WithDecodeFormat(YAML), WithFS(fstest.MapFS{
		"conf.d/base/config.yaml": {Data: []byte("value: 0\n")},
	}))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	generation := mgr.Snapshot().Generation()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(context.Background(), WithOverride(map[string]any{"value": 1 + i%2})); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if got := mgr.Snapshot().Generation(); got != generation+uint64(b.N) {
		b.Fatalf("committed %d generations, want %d", got-generation, b.N)
	}
}

func BenchmarkReloadDocument(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			doc := []byte(`{"payload":"` + strings.Repeat("x", size-len(`{"payload":""}`)) + `"}`)
			m, err := New[struct {
				Payload string `json:"payload"`
			}](context.Background(), WithFS(fstest.MapFS{
				"conf.d/base/config.json": &fstest.MapFile{Data: doc},
			}))
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			generation := m.Snapshot().Generation()
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := m.Reload(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if m.Snapshot().Generation() != generation {
				b.Fatal("unchanged document published a state")
			}
		})
	}
}

// BenchmarkSubscribeContention exercises the RWMutex path: 100 quiet
// subscribers (read side) compete with frequent Subscribe/cancel churn
// (write side) under continuous reload. The read path should run in
// parallel with itself, which this benchmark surfaces.
func BenchmarkSubscribeContention(b *testing.B) {
	const subscriberCount = 100
	mgr := newRuntimeManager(b)
	defer func() { _ = mgr.Close() }()
	var dummyA int
	for range subscriberCount {
		Subscribe(mgr,
			func(c *runtimeConfig) *int { return &c.A },
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
			func(c *runtimeConfig) *int { return &c.A },
			func(_, _ *int) {},
		)
		if err := mgr.Reload(ctx, WithOverride(map[string]any{
			"a": 2 + i%2,
		})); err != nil {
			b.Fatal(err)
		}
		cancel()
	}
	benchIntSink = dummyA
}

func BenchmarkReloadManySubscribers(b *testing.B) {
	for _, n := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			mgr := newRuntimeManager(b)
			defer func() { _ = mgr.Close() }()
			var sink int
			cancels := make([]func(), 0, n)
			for range n {
				cancels = append(cancels, Subscribe(mgr,
					func(c *runtimeConfig) *int { return &c.A },
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
				if err := mgr.Reload(ctx, WithOverride(map[string]any{
					"a": 2 + i%2,
				})); err != nil {
					b.Fatal(err)
				}
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
	defer func() { _ = mgr.Close() }()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

var (
	benchIntSink      int
	benchSettingsSink map[string]any
)

func BenchmarkStateMapCold(b *testing.B) {
	cfg := runtimeConfig{A: 1, B: "hello"}
	cfg.C.D = true
	cfg.C.E = "world"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state := &State[runtimeConfig]{value: &cfg}
		benchSettingsSink = state.Map()
	}
}

// BenchmarkReloadCommitObserver includes delivery of every Committed event
// and its diff computation.
func BenchmarkReloadCommitObserver(b *testing.B) {
	delivered := make(chan struct{}, 1)
	mgr, err := New[runtimeConfig](context.Background(),
		WithFS(fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("a: 1\nb: hello\nc:\n  d: true\n  e: world\n")}}),
		WithObserver(observerFunc(func(_ context.Context, e Event) {
			if c, ok := e.(Committed); ok {
				_ = c.Diff()
				delivered <- struct{}{}
			}
		})),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	// New emits the initial commit synchronously; leave room for measured commits.
	<-delivered
	generation := mgr.Snapshot().Generation()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := mgr.Reload(context.Background(), WithOverride(map[string]any{"a": 2 + i%2})); err != nil {
			b.Fatal(err)
		}
		<-delivered
	}
	b.StopTimer()
	if mgr.Snapshot().Generation() != generation+uint64(b.N) {
		b.Fatal("observer fixture skipped a commit")
	}
}
