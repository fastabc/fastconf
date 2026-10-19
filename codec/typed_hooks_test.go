package codec_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"regexp"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
)

type cfg struct {
	Timeout time.Duration `json:"timeout"`
	Server  struct {
		Idle time.Duration `json:"idle"`
	} `json:"server"`
}

func TestBuildTypedHookPlan_FindsNestedDuration(t *testing.T) {
	plan := codec.BuildTypedHookPlan(reflect.TypeOf(cfg{}), codec.DefaultTypedHooks())
	merged := map[string]any{
		"timeout": "30s",
		"server":  map[string]any{"idle": "5m"},
	}
	if err := plan.Apply(merged); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, ok := merged["timeout"].(time.Duration); !ok || got != 30*time.Second {
		t.Errorf("timeout = %v", merged["timeout"])
	}
	inner, _ := merged["server"].(map[string]any)
	if got, ok := inner["idle"].(time.Duration); !ok || got != 5*time.Minute {
		t.Errorf("idle = %v", inner["idle"])
	}
}

func TestApply_BadDurationReturnsError(t *testing.T) {
	plan := codec.BuildTypedHookPlan(reflect.TypeOf(cfg{}), codec.DefaultTypedHooks())
	merged := map[string]any{"timeout": "not-a-duration"}
	if err := plan.Apply(merged); err == nil {
		t.Error("expected duration parse error")
	}
}

func TestApply_NilPlanIsNoop(t *testing.T) {
	var plan *codec.TypedHookPlan
	if err := plan.Apply(map[string]any{"a": "b"}); err != nil {
		t.Errorf("nil plan should be no-op: %v", err)
	}
}

func TestDurationHook_NumericPassthrough(t *testing.T) {
	got, err := codec.DurationHook{}.Convert(int64(1000))
	if err != nil || got != int64(1000) {
		t.Errorf("numeric should pass through: got=%v err=%v", got, err)
	}
}

// StringPrimitiveHook end-to-end: env-style string values land in
// typed primitive struct fields through the typed-decode plan.
type primCfg struct {
	Port    int     `json:"port"`
	Enabled bool    `json:"enabled"`
	Rate    float64 `json:"rate"`
	Tag     string  `json:"tag"`
	// Duration must still win against StringPrimitiveHook because
	// DurationHook matches the named type first.
	Timeout time.Duration `json:"timeout"`
	Nested  struct {
		Workers uint32 `json:"workers"`
	} `json:"nested"`
}

func TestStringPrimitiveHook_ConvertsStringsInto(t *testing.T) {
	plan := codec.BuildTypedHookPlan(reflect.TypeOf(primCfg{}), codec.DefaultTypedHooks())
	merged := map[string]any{
		"port":    "8080",
		"enabled": "true",
		"rate":    "0.75",
		"tag":     "v1",
		"timeout": "5s",
		"nested":  map[string]any{"workers": "16"},
	}
	if err := plan.Apply(merged); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := merged["port"]; got != int64(8080) {
		t.Errorf("port = %v (%T), want int64(8080)", got, got)
	}
	if got := merged["enabled"]; got != true {
		t.Errorf("enabled = %v (%T), want bool true", got, got)
	}
	if got := merged["rate"]; got != float64(0.75) {
		t.Errorf("rate = %v (%T), want float64(0.75)", got, got)
	}
	if got := merged["tag"]; got != "v1" {
		t.Errorf("tag = %v, want v1 (string passes through untouched)", got)
	}
	if got := merged["timeout"]; got != 5*time.Second {
		t.Errorf("timeout = %v (%T), want time.Duration 5s (DurationHook precedence)", got, got)
	}
	inner, _ := merged["nested"].(map[string]any)
	if got := inner["workers"]; got != uint64(16) {
		t.Errorf("nested.workers = %v (%T), want uint64(16)", got, got)
	}
}

func TestStringPrimitiveHook_BadBoolReturnsError(t *testing.T) {
	type cfgBool struct {
		On bool `json:"on"`
	}
	plan := codec.BuildTypedHookPlan(reflect.TypeOf(cfgBool{}), codec.DefaultTypedHooks())
	merged := map[string]any{"on": "maybe"}
	if err := plan.Apply(merged); err == nil {
		t.Fatal("expected error for non-boolean string")
	}
}

func TestStringPrimitiveHook_NonStringPassthrough(t *testing.T) {
	type cfgInt struct {
		N int `json:"n"`
	}
	plan := codec.BuildTypedHookPlan(reflect.TypeOf(cfgInt{}), codec.DefaultTypedHooks())
	merged := map[string]any{"n": float64(42)}
	if err := plan.Apply(merged); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := merged["n"]; got != float64(42) {
		t.Errorf("n = %v (%T), want float64(42) passthrough", got, got)
	}
}

func TestTypedHookPlan_ExactFieldNameCandidate(t *testing.T) {
	type exactCfg struct {
		Timeout time.Duration `json:"timeout"`
	}
	plan := codec.BuildTypedHookPlan(reflect.TypeOf(exactCfg{}), codec.DefaultTypedHooks())
	merged := map[string]any{"Timeout": "2s"}
	if err := plan.Apply(merged); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := merged["Timeout"]; got != 2*time.Second {
		t.Fatalf("Timeout = %v (%T), want time.Duration", got, got)
	}
}

func TestIPHook(t *testing.T) {
	type config struct {
		IP net.IP `json:"ip"`
	}
	for _, tc := range []struct {
		input string
		valid bool
	}{{"10.0.0.1", true}, {"2001:db8::1", true}, {"x", false}} {
		st, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("ip: " + tc.input + "\n")}}), fastconf.WithTypedHook(codec.IPHook{}))
		if !tc.valid {
			if !errors.Is(err, fastconf.ErrTransform) {
				t.Fatalf("invalid IP: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !st.Value().IP.Equal(net.ParseIP(tc.input)) {
			t.Fatalf("IP = %v, want %s", st.Value().IP, tc.input)
		}
	}
}

func TestRegexHook(t *testing.T) {
	type config struct {
		Pattern *regexp.Regexp `json:"pattern"`
	}
	for _, tc := range []struct {
		input string
		valid bool
	}{{"^a+$", true}, {"(", false}} {
		st, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("pattern: '" + tc.input + "'\n")}}), fastconf.WithTypedHook(codec.RegexHook{}))
		if !tc.valid {
			if !errors.Is(err, fastconf.ErrTransform) {
				t.Fatalf("invalid regex: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Value().Pattern; got == nil || got.String() != tc.input || !got.MatchString("aaa") || got.MatchString("b") {
			t.Fatalf("Pattern = %v", got)
		}
	}
}
