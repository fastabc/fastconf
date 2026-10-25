package fastconf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/scan"
	"github.com/fastabc/fastconf/internal/secret"
)

func BenchmarkRedactionPlan(b *testing.B) {
	type item struct {
		Name  string `json:"name"`
		Token string `json:"token" fc:"secret"`
	}
	type config struct {
		Items []item `json:"items"`
	}
	cfg := config{Items: make([]item, 256)}
	for i := range cfg.Items {
		cfg.Items[i] = item{"service", "canary"}
	}
	m, err := newManager[config](context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(m.lifetimeCancel)
	s := m.newState(&pipelineState[config]{target: &cfg}, [32]byte{}, 1, ReloadCause{})
	warm := s.Map()
	if warm["items"].([]any)[0].(map[string]any)["token"] != "***REDACTED***" {
		b.Fatal("fixture did not redact its secret")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.Map()
	}
}

func BenchmarkRedactionPlanBuild(b *testing.B) {
	type config struct {
		Token string `fc:"secret"`
		Next  *config
	}
	for b.Loop() {
		secret.ApplyJSON(map[string]any{}, reflect.TypeFor[config](), nil)
	}
}

func BenchmarkUnknownWarnDecode(b *testing.B) {
	for _, yaml := range []bool{false, true} {
		b.Run(fmt.Sprintf("YAML=%v", yaml), func(b *testing.B) {
			m := &Manager[runtimeConfig]{opts: options{UnknownFields: UnknownWarn, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
			m.opts.YAMLBridge = yaml
			p := &pipelineState[runtimeConfig]{merged: map[string]any{"unknown": "value", "a": 8080}}
			b.ReportAllocs()
			for b.Loop() {
				if err := runDecode(context.Background(), m, p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDiffDeep(b *testing.B) {
	build := func(value string) map[string]any {
		root := map[string]any{}
		for i := 0; i < 32; i++ {
			node := map[string]any{}
			root[fmt.Sprint(i)] = node
			for j := 1; j < 8; j++ {
				child := map[string]any{}
				node["child"] = child
				node = child
			}
			node["value"] = value
		}
		return root
	}
	a := build("before")
	for _, mode := range []string{"equal", "sparse", "dense"} {
		b.Run(mode, func(b *testing.B) {
			other := build("before")
			if mode == "dense" {
				other = build("after")
			}
			if mode == "sparse" {
				node := other["0"].(map[string]any)
				for j := 1; j < 8; j++ {
					node = node["child"].(map[string]any)
				}
				node["value"] = "after"
			}
			want := map[string]int{"equal": 0, "sparse": 1, "dense": 32}[mode]
			if got := len(diffMaps(nil, a, other)); got != want {
				b.Fatalf("fixture has %d differences, want %d", got, want)
			}
			b.ReportAllocs()
			for b.Loop() {
				diffMaps(nil, a, other)
			}
		})
	}
}

func BenchmarkSubscriberChurn(b *testing.B) {
	m := newRuntimeManager(b)
	for i := 0; i < 50; i++ {
		Subscribe(m, func(c *runtimeConfig) *int { return &c.A }, func(_, _ *int) {})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cancel := Subscribe(m, func(c *runtimeConfig) *int { return &c.A }, func(_, _ *int) {})
		cancel()
	}
}

// One batch changes the same 256 KiB document 32 times. live-B measures
// retained cache data after GC, separately from allocation throughput.
func BenchmarkLayerCacheVersions(b *testing.B) {
	dec, err := codec.For("json")
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var cache layerCache
		for version := 0; version < 32; version++ {
			data := []byte(fmt.Sprintf(`{"payload":%q,"version":%d}`, strings.Repeat("x", 256<<10), version))
			layer := scan.Layer{Path: "same.json", Codec: "json", Bytes: data}
			if _, err := cache.decoded(layer, sha256.Sum256(data), dec, 0); err != nil {
				b.Fatal(err)
			}
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)), "live-B")
		runtime.KeepAlive(&cache)
	}
}

// Alternating measures the explicit retention tradeoff: reverting to an older
// document decodes it again when only the latest successful version is cached.
func BenchmarkLayerCacheAlternating(b *testing.B) {
	dec, err := codec.For("json")
	if err != nil {
		b.Fatal(err)
	}
	layers := []scan.Layer{
		{Path: "same.json", Codec: "json", Bytes: []byte(fmt.Sprintf(`{"payload":%q,"version":0}`, strings.Repeat("x", 256<<10)))},
		{Path: "same.json", Codec: "json", Bytes: []byte(fmt.Sprintf(`{"payload":%q,"version":1}`, strings.Repeat("x", 256<<10)))},
	}
	sums := [][32]byte{sha256.Sum256(layers[0].Bytes), sha256.Sum256(layers[1].Bytes)}
	var cache layerCache
	for i, layer := range layers {
		if _, err := cache.decoded(layer, sums[i], dec, 0); err != nil {
			b.Fatal(err)
		}
	}
	next := 0
	b.ReportAllocs()
	for b.Loop() {
		if _, err := cache.decoded(layers[next], sums[next], dec, 0); err != nil {
			b.Fatal(err)
		}
		next ^= 1
	}
}
