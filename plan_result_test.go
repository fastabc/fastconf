package fastconf

import (
	"errors"
	"testing"

	"github.com/fastabc/fastconf/policy"
)

func TestPlanResultErr(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	for _, tc := range []struct {
		name    string
		plan    *PlanResult[struct{}]
		invalid bool
	}{
		{"nil", nil, true},
		{"incomplete", &PlanResult[struct{}]{}, true},
		{"valid", &PlanResult[struct{}]{Proposed: &State[struct{}]{}}, false},
		{"warning", &PlanResult[struct{}]{Proposed: &State[struct{}]{}, Policies: []policy.Violation{{Severity: policy.SeverityWarning}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.plan.Err()
			if (err != nil) != tc.invalid || (tc.invalid && !errors.Is(err, ErrInvalid)) {
				t.Fatalf("Err() = %v", err)
			}
		})
	}
	p := &PlanResult[struct{}]{Proposed: &State[struct{}]{},
		Validators: []ValidatorReport{{Name: "first", Err: first}, {Name: "pass"}, {Name: "second", Err: second}},
		Policies:   []policy.Violation{{Rule: "warn", Severity: policy.SeverityWarning}, {Rule: "deny", Severity: policy.SeverityError}}}
	err := p.Err()
	for _, want := range []error{first, second, ErrInvalid, ErrFastConf} {
		if !errors.Is(err, want) {
			t.Fatalf("%v does not wrap %v", err, want)
		}
	}
	var denied *PolicyError
	if !errors.As(err, &denied) || len(denied.Violations) != 1 || denied.Violations[0].Rule != "deny" {
		t.Fatalf("missing denial: %v", err)
	}
	denied.Violations[0].Rule = "changed"
	if len(p.Validators) != 3 || len(p.Policies) != 2 || p.Policies[1].Rule != "deny" {
		t.Fatal("Err mutated original reports")
	}
}
