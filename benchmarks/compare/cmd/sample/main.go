// Command sample records individual reload latencies in a standalone process.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	compare "github.com/fastabc/fastconf/benchmarks/compare"
)

func run() error {
	name := flag.String("adapter", "FastConf", "FastConf, Viper or Koanf")
	size := flag.Int("size", 4096, "fixture bytes")
	count := flag.Int("samples", 100, "individual reload samples")
	mode := flag.String("mode", "valid", "valid, noop or invalid")
	flag.Parse()
	if *count < 1 || *size < 4096 || (*mode != "valid" && *mode != "noop" && *mode != "invalid") {
		return fmt.Errorf("invalid sample parameters")
	}
	first, second, invalid := compare.Fixture(*size, 1), compare.Fixture(*size, 2), compare.Fixture(*size, 0)
	a, err := compare.Open(*name, first)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	latencies := make([]int64, *count)
	for i := range latencies {
		data := first
		if *mode == "valid" && i%2 == 0 {
			data = second
		}
		if *mode == "invalid" {
			data = invalid
		}
		start := time.Now()
		err := a.Reload(data)
		latencies[i] = time.Since(start).Nanoseconds()
		if (err != nil) != (*mode == "invalid") {
			return fmt.Errorf("unexpected reload: %v", err)
		}
	}
	runtime.ReadMemStats(&after)
	ordered := append([]int64(nil), latencies...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	percentile := func(p int) int64 { return ordered[(len(ordered)*p+99)/100-1] }
	return json.NewEncoder(os.Stdout).Encode(struct {
		Adapter   string  `json:"adapter"`
		Mode      string  `json:"mode"`
		Size      int     `json:"size_bytes"`
		Samples   []int64 `json:"latency_ns"`
		P50       int64   `json:"p50_ns"`
		P95       int64   `json:"p95_ns"`
		P99       int64   `json:"p99_ns"`
		Allocated uint64  `json:"allocated_bytes"`
		GCs       uint32  `json:"gc_cycles"`
	}{*name, *mode, *size, latencies, percentile(50), percentile(95), percentile(99), after.TotalAlloc - before.TotalAlloc, after.NumGC - before.NumGC})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
