package policy_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/policy"
)

type policyConfig struct {
	Debug bool `json:"debug"`
}

func TestPolicy_RejectsInitialConfig(t *testing.T) {
	deny := policy.Func[policyConfig]{N: "deny", Fn: func(context.Context, policy.Input[policyConfig]) ([]policy.Violation, error) {
		return []policy.Violation{{Message: "denied", Severity: policy.SeverityError}}, nil
	}}
	snapshot, err := fastconf.Load[policyConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("debug: true")}}), fastconf.WithPolicy(deny))
	var denied *fastconf.PolicyError
	if snapshot != nil || !errors.Is(err, fastconf.ErrInvalid) || !errors.As(err, &denied) {
		t.Fatalf("Load = %v, %v; want nil, PolicyError", snapshot, err)
	}
	if len(denied.Violations) != 1 || denied.Violations[0].Rule != "deny" {
		t.Fatalf("findings = %+v", denied.Violations)
	}
}

func TestPolicy_PreviewAndPublication(t *testing.T) {
	for _, tc := range []struct {
		name          string
		findings      []policy.Violation
		evaluationErr error
		reject        bool
	}{
		{name: "allow"},
		{name: "warning", findings: []policy.Violation{{Path: "debug", Message: "warning", Severity: policy.SeverityWarning}}},
		{name: "deny", findings: []policy.Violation{{Path: "debug", Message: "denied", Severity: policy.SeverityError}}, reject: true},
		{name: "mixed", findings: []policy.Violation{{Rule: "explicit", Path: "debug", Severity: policy.SeverityWarning}, {Path: "debug", Severity: policy.SeverityError}}, reject: true},
		{name: "evaluation error", evaluationErr: errors.New("backend unavailable"), reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("debug: false")}}
			p := policy.Func[policyConfig]{N: "guard", Fn: func(_ context.Context, in policy.Input[policyConfig]) ([]policy.Violation, error) {
				if !in.Config.Debug {
					return nil, nil
				}
				return tc.findings, tc.evaluationErr
			}}
			m, err := fastconf.New[policyConfig](context.Background(), fastconf.WithFS(fs), fastconf.WithPolicy(p))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			before := m.Snapshot()
			fs["conf.d/base/app.yaml"].Data = []byte("debug: true")
			preview, err := m.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if m.Snapshot() != before || !preview.Proposed.Value().Debug {
				t.Fatal("Plan must return the candidate without publishing")
			}
			want := slices.Clone(tc.findings)
			if tc.evaluationErr != nil {
				want = []policy.Violation{{Rule: "guard", Message: "evaluation error: " + tc.evaluationErr.Error(), Severity: policy.SeverityError}}
			}
			for i := range want {
				if want[i].Rule == "" {
					want[i].Rule = "guard"
				}
			}
			if !reflect.DeepEqual(preview.Policies, want) {
				t.Fatalf("Plan policies = %+v; want %+v", preview.Policies, want)
			}
			err = m.Reload(context.Background())
			if tc.reject {
				var denied *fastconf.PolicyError
				if !errors.Is(err, fastconf.ErrInvalid) || !errors.As(err, &denied) {
					t.Fatalf("Reload = %v; want PolicyError", err)
				}
				if m.Snapshot() != before {
					t.Fatal("denied reload published")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !m.Get().Debug || m.Snapshot().Generation() != before.Generation()+1 {
					t.Fatal("allowed reload did not publish")
				}
			}
		})
	}
}

func TestWithPolicy_RegistrationOrder(t *testing.T) {
	var ran []string
	p := func(name string) policy.Func[policyConfig] {
		return policy.Func[policyConfig]{N: name, Fn: func(context.Context, policy.Input[policyConfig]) ([]policy.Violation, error) {
			ran = append(ran, name)
			return nil, nil
		}}
	}
	_, err := fastconf.Load[policyConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("debug: false")}}), fastconf.WithPolicy(p("a")), fastconf.WithPolicy(p("b"), p("c")))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"a", "b", "c"}) {
		t.Fatalf("policies = %v; want [a b c]", ran)
	}
}
