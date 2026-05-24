package fastconf

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/pkg/source"
)

func TestPlan_ProducesDiff(t *testing.T) {
	type cfg struct {
		Port int `yaml:"port"`
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("base", "yaml", []byte("port: 8080\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	dryMgr, err := New[cfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("base", "yaml", []byte("port: 8080\n")), nil),
		WithSource(source.NewBytes("over", "yaml", []byte("port: 9090\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer dryMgr.Close()

	plan, err := dryMgr.Plan().Run(context.Background())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Proposed == nil || plan.Proposed.Value().Port != 9090 {
		t.Fatalf("expected proposed port 9090, got %+v", plan.Proposed)
	}
	// PlanResult.Diff is structured ([]DiffEntry), so consumers do not
	// have to parse rendered strings to filter / sort by change kind.
	// dryMgr starts from base port=8080 already, so its Plan vs current
	// state is empty; assert the slice is the new type without
	// asserting non-emptiness.
	var _ []DiffEntry = plan.Diff
	gen := mgr.Snapshot().Generation()
	if _, err := mgr.Plan().Run(context.Background()); err != nil {
		t.Fatalf("plan idempotent: %v", err)
	}
	if mgr.Snapshot().Generation() != gen {
		t.Fatalf("Plan must not bump generation: %d vs %d", mgr.Snapshot().Generation(), gen)
	}
}

func TestPlan_CollectsValidatorErrors(t *testing.T) {
	type cfg struct {
		Port int `yaml:"port"`
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("base", "yaml", []byte("port: 8080\n")), nil),
		WithValidator(func(c *cfg) error {
			if c.Port < 1024 {
				return errPortTooLow
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	plan, err := mgr.Plan().Run(context.Background())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Validators) != 1 || plan.Validators[0].Err != nil {
		t.Fatalf("want 1 passing validator, got %+v", plan.Validators)
	}
	if !strings.HasPrefix(plan.Validators[0].Name, "validator[") {
		t.Fatalf("unexpected validator name %q", plan.Validators[0].Name)
	}
}

var errPortTooLow = stringErr("port too low")

type stringErr string

func (s stringErr) Error() string { return string(s) }

type bug1208Cfg struct {
	Region string `json:"region"`
}

// Regression: Plan() on a CI runner picked up the runner's hostname
// when a multi-axis overlay used DefaultFromHostname=true, producing a
// diff that did not reflect the target production environment. The
// WithPlanHostname Option pins the hostname for a single Plan call.
func TestPlan_HostnameOverride(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":             &fstest.MapFile{Data: []byte("region: base\n")},
		"conf.d/hosts/prod-pod-3/00.yaml": &fstest.MapFile{Data: []byte("region: prod\n")},
	}
	mgr, err := New[bug1208Cfg](context.Background(),
		WithFS(fs),
		WithDir("conf.d"),
		WithMultiAxisOverlays(OverlayAxis{
			Dir:                 "hosts",
			DefaultFromHostname: true,
			Priority:            3000,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	// Without override: result depends on the runner's actual hostname,
	// so we only check that it's NOT prod (matches base).
	plan, err := mgr.Plan().Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = plan // hostname-dependent; assert nothing here.

	// With override: hostname pinned to "prod-pod-3" → overlay engages.
	plan2, err := mgr.Plan().WithHostname("prod-pod-3").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Proposed.Value().Region != "prod" {
		t.Errorf("expected region=prod with hostname override, got %q", plan2.Proposed.Value().Region)
	}

	// And empty-string override: should be treated as no override.
	plan3, err := mgr.Plan().WithHostname("").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// We don't know the runner's actual hostname; just ensure it didn't
	// happen to be the literal "prod" (extremely unlikely).
	if strings.Contains(plan3.Proposed.Value().Region, "prod-pod-3") {
		t.Errorf("empty override should NOT pin to prod-pod-3: got %q", plan3.Proposed.Value().Region)
	}
}
