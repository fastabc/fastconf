package fastconf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

type nilCfg struct {
	Name string `json:"name"`
}

func TestStateCauseRevisionsAreDetached(t *testing.T) {
	s := &State[nilCfg]{cause: ReloadCause{Revisions: map[string]string{"remote": "r1"}}}
	cause := s.Cause()
	cause.Revisions["remote"] = "changed"
	delete(cause.Revisions, "remote")
	if got := s.Cause().Revisions["remote"]; got != "r1" {
		t.Fatalf("Cause mutation changed snapshot revision: %q", got)
	}
}

func TestState_NilSafety(t *testing.T) {
	var s *State[nilCfg] // intentionally nil

	t.Run("Map", func(t *testing.T) {
		if got := s.Map(); got != nil {
			t.Errorf("Map on nil: want nil, got %v", got)
		}
		if got := s.Unredacted().Map(); got != nil {
			t.Errorf("Unredacted().Map on nil: want nil, got %v", got)
		}
	})

	t.Run("Explain", func(t *testing.T) {
		if got := s.Explain("any.path"); got != nil {
			t.Errorf("Explain on nil: want nil, got %v", got)
		}
	})

	t.Run("Diff", func(t *testing.T) {
		// nil vs nil → no differences
		if got := s.Diff(nil); len(got) != 0 {
			t.Errorf("nil.Diff(nil): want empty, got %v", got)
		}
	})

	t.Run("Dump", func(t *testing.T) {
		for _, tc := range []struct {
			format Format
			want   string
		}{{JSON, "{}"}, {YAML, "{}\n"}, {TOML, ""}} {
			t.Run(string(tc.format), func(t *testing.T) {
				for _, dump := range []func(Format) ([]byte, error){s.Dump, s.Unredacted().Dump} {
					b, err := dump(tc.format)
					if err != nil || string(b) != tc.want {
						t.Errorf("Dump on nil = %q, %v; want %q, nil", b, err, tc.want)
					}
				}
			})
		}
	})
}

func TestState_SourcesAreCopies(t *testing.T) {
	s := &State[int]{sources: []SourceRef{{Path: "original", Priority: 1}}}
	sources := s.Sources()
	sources[0] = SourceRef{Path: "mutated", Priority: 99999}
	if got := s.Sources(); len(got) != 1 || got[0] != s.sources[0] || got[0].Path != "original" || got[0].Priority != 1 {
		t.Fatalf("Sources leaked storage: %+v", got)
	}
	if s.sourceCount() != 1 {
		t.Fatalf("sourceCount = %d; want 1", s.sourceCount())
	}
	var empty *State[int]
	if empty.Sources() != nil || empty.sourceCount() != 0 {
		t.Fatal("nil state source accessors must return nil/zero")
	}
}

// mapsEqualJSON compares two JSON-decoded maps by re-marshalling to a
// stable form. Sufficient for parity tests where ordering is the only
// expected variance.
func mapsEqualJSON(a, b map[string]any) bool {
	pa, _ := json.Marshal(a)
	pb, _ := json.Marshal(b)
	return bytes.Equal(pa, pb)
}

type snapshotConfig struct {
	Name string `json:"name" yaml:"name"`
	DB   struct {
		DSN  string `json:"dsn" yaml:"dsn"`
		Pool int    `json:"pool" yaml:"pool"`
	} `json:"db" yaml:"db"`
}

// emptyFS provides an empty conf.d so the file-discovery layer produces no
// layers, leaving the configured provider as the only contributor.
func emptyFS() fstest.MapFS {
	return fstest.MapFS{
		"conf.d/base/.keep": &fstest.MapFile{Data: []byte{}},
	}
}

func waitCommittedReason(t *testing.T, ch <-chan Committed, reason string) Committed {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Cause.Reason == reason {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for Committed reason %q", reason)
		}
	}
}

func TestDiagnosticNumbersAreLossless(t *testing.T) {
	type config struct {
		ID       int64  `json:"id"`
		Unsigned uint64 `json:"unsigned"`
	}
	m, err := New[config](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("numbers", "json", []byte(`{"id":9007199254740992}`))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"id": int64(9007199254740993)})); err != nil {
		t.Fatal(err)
	}
	if len(before.Diff(m.Snapshot())) != 1 {
		t.Fatal("integer increment disappeared from diff")
	}
	if got := fmt.Sprint(m.Snapshot().Unredacted().Map()["id"]); got != "9007199254740993" {
		t.Fatalf("Map rounded integer: %s", got)
	}
	for _, format := range []Format{JSON, YAML, TOML} {
		b, err := m.Snapshot().Unredacted().Dump(format)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "9007199254740993") || strings.Contains(string(b), `"9007199254740993"`) {
			t.Fatalf("%s changed number: %s", format, b)
		}
	}
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"id": int64(-9223372036854775808), "unsigned": uint64(18446744073709551615)})); err != nil {
		t.Fatal(err)
	}
	for _, format := range []Format{JSON, YAML} {
		b, err := m.Snapshot().Unredacted().Dump(format)
		if err != nil || !strings.Contains(string(b), "18446744073709551615") || !strings.Contains(string(b), "-9223372036854775808") {
			t.Fatalf("boundary lost in %s", format)
		}
	}
	if _, err := m.Snapshot().Unredacted().Dump(TOML); err == nil {
		t.Fatal("TOML must reject integers outside signed 64-bit range")
	}
}

func TestMapNumbersStayExact(t *testing.T) {
	m, err := New[map[string]any](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("numbers", "json", []byte(`{"id":9007199254740993,"decimal":1.234567890123456789,"exponent":1e30}`))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	for k, want := range map[string]string{"id": "9007199254740993", "decimal": "1.234567890123456789", "exponent": "1e30"} {
		got, ok := (*m.Get())[k].(json.Number)
		if !ok || got.String() != want {
			t.Fatalf("%s lost numeric representation", k)
		}
	}
}

type countedJSONValue struct {
	calls   *atomic.Int64
	payload map[string]any
	fail    bool
}

func (v countedJSONValue) MarshalJSON() ([]byte, error) {
	v.calls.Add(1)
	if v.fail {
		return nil, errors.New("cannot encode")
	}
	return json.Marshal(v.payload)
}

func cachedTestState() (*State[countedJSONValue], *atomic.Int64) {
	calls := new(atomic.Int64)
	v := &countedJSONValue{calls: calls, payload: map[string]any{"items": []any{map[string]any{"value": "original"}}}}
	return &State[countedJSONValue]{value: v}, calls
}

func mutateCachedView(raw map[string]any) {
	raw["items"].([]any)[0].(map[string]any)["value"] = "mutated"
}

func TestTreeCacheMemoizationAndDetachedViews(t *testing.T) {
	s, calls := cachedTestState()
	mutateCachedView(s.Map())
	diff := s.Diff(nil)
	diff[0].Before.([]any)[0].(map[string]any)["value"] = "diff mutation"
	copy := restampState(s, 2, ReloadCause{})
	mutateCachedView(copy.Map())
	for _, state := range []*State[countedJSONValue]{s, copy} {
		dump, err := state.Unredacted().Dump(JSON)
		if err != nil || !strings.Contains(string(dump), "original") || strings.Contains(string(dump), "mutation") {
			t.Fatalf("dump=%s err=%v", dump, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("marshal calls=%d want=1", calls.Load())
	}
}

func TestTreeCacheConcurrentViews(t *testing.T) {
	s, _ := cachedTestState()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				mutateCachedView(s.Map())
				diff := s.Diff(nil)
				diff[0].Before.([]any)[0].(map[string]any)["value"] = "diff mutation"
				dump, err := s.Unredacted().Dump(JSON)
				if err != nil || !strings.Contains(string(dump), "original") {
					t.Errorf("dump=%s err=%v", dump, err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestTreeCacheEncodingFailuresRemainErrors(t *testing.T) {
	s, calls := cachedTestState()
	s.value.fail = true
	for i := 0; i < 2; i++ {
		if _, err := s.Unredacted().Dump(JSON); err == nil {
			t.Fatal("encoding failure hidden")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("encoding failure was cached: calls=%d", calls.Load())
	}
}

func TestTreeCacheRedactorIsolationAndSecretDiff(t *testing.T) {
	type cfg struct {
		Tokens []map[string]string `json:"tokens" fc:"secret"`
		Value  string              `json:"value"`
	}
	// A custom redactor may mutate a container argument and return it directly.
	mutating := func(_ string, v any) any { v.([]any)[0].(map[string]any)["token"] = "custom"; return v }
	before := &State[cfg]{value: &cfg{Tokens: []map[string]string{{"token": "before-secret"}}, Value: "unchanged"}, redactor: mutating}
	after := &State[cfg]{value: &cfg{Tokens: []map[string]string{{"token": "after-secret"}}, Value: "unchanged"}}
	view := before.Map()
	view["tokens"].([]any)[0].(map[string]any)["token"] = "caller mutation"
	diff := diagnosticDiff(before, after)
	if len(diff) != 1 || diff[0].Path != "tokens" {
		t.Fatalf("secret-only change lost: %+v", diff)
	}
	encoded, _ := json.Marshal(diff)
	if strings.Contains(string(encoded), "before-secret") || strings.Contains(string(encoded), "after-secret") {
		t.Fatalf("secret leaked: %s", encoded)
	}
	raw, err := before.Unredacted().Dump(JSON)
	if err != nil || !strings.Contains(string(raw), "before-secret") || strings.Contains(string(raw), "custom") {
		t.Fatalf("cache mutated by redactor: %s %v", raw, err)
	}
}

type hashConfig struct {
	B string `json:"b"`
	A int    `json:"a"`
}

func TestState_HashIgnoresUndecodedFields(t *testing.T) {
	cfg := &hashConfig{B: "hello", A: 1}
	if err := decodeInto(map[string]any{"a": 1, "b": "hello", "extra": true}, cfg, false, false); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalHash(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(`{"b":"hello","a":1}`))
	if got != want {
		t.Fatalf("hash mismatch: got %x want %x", got, want)
	}
}

// The final value has a stable hash, and a different value changes it.
func TestCanonicalHashDeterministic(t *testing.T) {
	type cfg struct {
		Port int `json:"port"`
	}
	target := &cfg{Port: 8080}

	h1, err := canonicalHash(target, false)
	if err != nil {
		t.Fatalf("canonicalHash (1st call): %v", err)
	}
	h2, err := canonicalHash(target, false)
	if err != nil {
		t.Fatalf("canonicalHash (2nd call): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %x != %x", h1, h2)
	}
	// Sanity: a different value must produce a different hash.
	other := &cfg{Port: 9090}
	h3, err := canonicalHash(other, false)
	if err != nil {
		t.Fatalf("canonicalHash (3rd call): %v", err)
	}
	if h1 == h3 {
		t.Fatal("expected different hashes for different targets; got same")
	}
}

type finalHashConfig struct {
	Value int `json:"value"`
}

type yamlHashConfig struct {
	Labels map[string]string `yaml:"labels"`
	Fail   bool              `yaml:"fail"`
}

var errYAMLHash = errors.New("YAML hash failed")

func (c yamlHashConfig) MarshalYAML() (any, error) {
	if c.Fail {
		return nil, errYAMLHash
	}
	return c.Labels, nil
}

func TestCanonicalHashYAMLMarshaler(t *testing.T) {
	value := yamlHashConfig{Labels: map[string]string{"z": "last", "a": "first"}}
	want := sha256.Sum256([]byte("a: first\nz: last\n"))
	for range 20 {
		got, err := canonicalHash(&value, true)
		if err != nil || got != want {
			t.Fatalf("custom map marshaler hash = %x, %v; want %x", got, err, want)
		}
	}
	value.Labels["a"] = "changed"
	if got, err := canonicalHash(&value, true); err != nil || got == want {
		t.Fatalf("changed marshaler output hash = %x, %v", got, err)
	}
}

func TestYAMLHashFailurePreservesState(t *testing.T) {
	fs := fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("labels: {a: first}\n")}}
	m, err := New[yamlHashConfig](context.Background(), WithFS(fs), WithDecodeFormat(YAML))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("labels: {a: changed}\nfail: true\n")}
	if _, err := m.Plan(context.Background()); !errors.Is(err, errYAMLHash) {
		t.Fatalf("Plan error = %v, want marshaler error", err)
	}
	if err := m.Reload(context.Background()); !errors.Is(err, errYAMLHash) {
		t.Fatalf("Reload error = %v, want marshaler error", err)
	}
	if m.Snapshot() != before {
		t.Fatal("hash error replaced the last good snapshot")
	}
}

type finalHashDecoded finalHashConfig

var finalHashVersion atomic.Int64

func (c *finalHashDecoded) UnmarshalJSON([]byte) error {
	c.Value = int(finalHashVersion.Load())
	return nil
}

func TestHashTracksFinalValue(t *testing.T) {
	t.Run("struct defaults", func(t *testing.T) {
		finalHashVersion.Store(1)
		checkFinalHash(t, func(c *finalHashConfig) bool { return c.Value == 2 })
	})
	t.Run("map defaults", func(t *testing.T) {
		finalHashVersion.Store(1)
		checkFinalHash(t, func(c *finalHashMap) bool { return (*c)["value"] == 2 })
	})
	t.Run("custom decoder", func(t *testing.T) {
		finalHashVersion.Store(1)
		checkFinalHash(t, func(c *finalHashDecoded) bool { return c.Value == 2 })
	})
}

func checkFinalHash[T any](t *testing.T, valid func(*T) bool) {
	t.Helper()
	m, err := New[T](context.Background(), WithFS(emptyFS()), WithProvider(source.NewBytes("input", "json", []byte(`{}`))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	finalHashVersion.Store(2)
	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !valid(plan.Proposed.Value()) {
		t.Fatal("preview value incorrect")
	}
	if m.Snapshot() != before {
		t.Fatal("preview published")
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := m.Snapshot()
	if !valid(after.Value()) || after.Generation() != before.Generation()+1 {
		t.Fatal("final computed value was not published")
	}
	b, err := json.Marshal(after.Value())
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(b)
	if after.Hash() != want || plan.Proposed.Hash() != want {
		t.Fatal("hash does not describe final value")
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != after {
		t.Fatal("same final value published twice")
	}
	err = m.Reload(context.Background(), WithOverride(map[string]any{"ignored": true}))
	if _, isMap := any(after.Value()).(*finalHashMap); isMap {
		if !errors.Is(err, ErrDecode) {
			t.Fatalf("map override = %v; want ErrDecode", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != after {
		t.Fatal("ignored or rejected override changed the snapshot")
	}
}

type finalHashMap map[string]int

func (c *finalHashConfig) Defaults() { c.Value = int(finalHashVersion.Load()) }

func (c *finalHashMap) Defaults() {
	if *c == nil {
		*c = finalHashMap{}
	}
	(*c)["value"] = int(finalHashVersion.Load())
}

func TestRingBuffer_CircularPush(t *testing.T) {
	r := newRing[State[int]](3)
	mk := func(g uint64) *State[int] { return &State[int]{generation: g} }
	for g := uint64(1); g <= 5; g++ {
		r.Push(mk(g))
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3, got %d", len(snap))
	}
	want := []uint64{3, 4, 5}
	for i, s := range snap {
		if s.generation != want[i] {
			t.Fatalf("idx %d: want %d got %d", i, want[i], s.generation)
		}
	}
	matchGen := func(g uint64) func(*State[int]) bool {
		return func(s *State[int]) bool { return s.generation == g }
	}
	if r.Find(matchGen(4)) == nil || r.Find(matchGen(1)) != nil {
		t.Fatalf("Find broken")
	}
}

func TestDumpTOMLNullInArrayNamesPath(t *testing.T) {
	for _, tc := range []struct{ yaml, path string }{
		{"l: [1, null]\n", "l[1]"},
		{"outer:\n  l: [[1, null]]\n", "outer.l[0][1]"},
	} {
		s, err := Load[map[string]any](context.Background(), WithFS(fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte(tc.yaml)}}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Dump(TOML)
		if err == nil || !strings.Contains(err.Error(), tc.path) || strings.Contains(err.Error(), "toml: toml:") {
			t.Fatalf("Dump(TOML) = %v, want path %s", err, tc.path)
		}
	}
}

func TestDumpTOMLOmitsMapNull(t *testing.T) {
	s, err := Load[map[string]any](context.Background(), WithFS(fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("keep: 1\nomit: null\nnested:\n  omit: null\n")}}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Dump(TOML)
	if err != nil || strings.Contains(string(out), "omit") || !strings.Contains(string(out), "keep = 1") {
		t.Fatalf("Dump(TOML) = %q, %v", out, err)
	}
}
