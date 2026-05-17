package decoder_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/fastabc/fastconf/pkg/decoder"
)

type cfg struct {
	Timeout time.Duration `json:"timeout"`
	Server  struct {
		Idle time.Duration `json:"idle"`
	} `json:"server"`
}

func TestBuildTypedHookPlan_FindsNestedDuration(t *testing.T) {
	plan := decoder.BuildTypedHookPlan(reflect.TypeOf(cfg{}), decoder.DefaultTypedHooks())
	merged := map[string]any{
		"timeout": "30s",
		"server":  map[string]any{"idle": "5m"},
	}
	if err := plan.Apply(merged); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, ok := merged["timeout"].(int64); !ok || got != int64(30*time.Second) {
		t.Errorf("timeout = %v", merged["timeout"])
	}
	inner, _ := merged["server"].(map[string]any)
	if got, ok := inner["idle"].(int64); !ok || got != int64(5*time.Minute) {
		t.Errorf("idle = %v", inner["idle"])
	}
}

func TestApply_BadDurationReturnsError(t *testing.T) {
	plan := decoder.BuildTypedHookPlan(reflect.TypeOf(cfg{}), decoder.DefaultTypedHooks())
	merged := map[string]any{"timeout": "not-a-duration"}
	if err := plan.Apply(merged); err == nil {
		t.Error("expected duration parse error")
	}
}

func TestApply_NilPlanIsNoop(t *testing.T) {
	var plan *decoder.TypedHookPlan
	if err := plan.Apply(map[string]any{"a": "b"}); err != nil {
		t.Errorf("nil plan should be no-op: %v", err)
	}
}

func TestDurationHook_NumericPassthrough(t *testing.T) {
	got, err := decoder.DurationHook{}.Convert(int64(1000))
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
	plan := decoder.BuildTypedHookPlan(reflect.TypeOf(primCfg{}), decoder.DefaultTypedHooks())
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
	if got := merged["timeout"]; got != int64(5*time.Second) {
		t.Errorf("timeout = %v, want 5s as int64 nanos (DurationHook precedence)", got)
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
	plan := decoder.BuildTypedHookPlan(reflect.TypeOf(cfgBool{}), decoder.DefaultTypedHooks())
	merged := map[string]any{"on": "maybe"}
	if err := plan.Apply(merged); err == nil {
		t.Fatal("expected error for non-boolean string")
	}
}

func TestStringPrimitiveHook_NonStringPassthrough(t *testing.T) {
	type cfgInt struct {
		N int `json:"n"`
	}
	plan := decoder.BuildTypedHookPlan(reflect.TypeOf(cfgInt{}), decoder.DefaultTypedHooks())
	merged := map[string]any{"n": float64(42)}
	if err := plan.Apply(merged); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := merged["n"]; got != float64(42) {
		t.Errorf("n = %v (%T), want float64(42) passthrough", got, got)
	}
}
