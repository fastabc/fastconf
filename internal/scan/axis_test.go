package scan

import (
	"errors"
	"os"
	"testing"
)

func TestResolveAxes_RejectsUnsafeValues(t *testing.T) {
	for _, v := range []string{"..", ".", "../x", "a/b", "/etc", `a\b`} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("FC_AXIS", v)
			out, errs := ResolveAxes([]AxisSpec{{Dir: "region", Env: "FC_AXIS"}}, os.Hostname)
			if len(out) != 0 || len(errs) != 1 || !errors.Is(errs[0].Err, errUnsafeAxisValue) {
				t.Fatalf("out=%v errs=%v", out, errs)
			}
		})
	}
	t.Setenv("FC_AXIS", "eu-west-1.prod")
	if out, errs := ResolveAxes([]AxisSpec{{Dir: "nested/regions", Env: "FC_AXIS"}}, os.Hostname); len(out) != 1 || len(errs) != 0 || out[0].Dir != "nested/regions/eu-west-1.prod" {
		t.Fatalf("safe value rejected: %v %v", out, errs)
	}
	t.Setenv("FC_AXIS", "")
	if out, errs := ResolveAxes([]AxisSpec{{Dir: "region", Env: "FC_AXIS", FromHostname: true}}, os.Hostname); len(out) != 0 || len(errs) != 0 {
		t.Fatalf("empty value must disable the axis: %v %v", out, errs)
	}
	out, errs := ResolveAxes([]AxisSpec{{Dir: "hosts", FromHostname: true}}, func() (string, error) { return "../x", nil })
	if len(out) != 0 || len(errs) != 1 || !errors.Is(errs[0].Err, errUnsafeAxisValue) {
		t.Fatalf("unsafe hostname accepted: %v %v", out, errs)
	}
}
