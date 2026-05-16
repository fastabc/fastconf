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
