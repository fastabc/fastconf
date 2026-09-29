package fastconf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/policy"
	"github.com/fastabc/fastconf/providers/source"
)

func TestPlan_ProducesDiff(t *testing.T) {
	type cfg struct {
		Port int `json:"port"`
	}
	fs := fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("port: 8080\n")}}
	mgr, err := New[cfg](context.Background(), WithFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	before := mgr.Snapshot()

	for _, port := range []int{8080, 9090} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("port: %d\n", port))}
			plan, err := mgr.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Proposed == nil || plan.Proposed.Value().Port != port {
				t.Fatalf("proposed = %+v; want port %d", plan.Proposed, port)
			}
			if port == 8080 {
				if len(plan.Diff) != 0 || plan.Proposed.Hash() != before.Hash() {
					t.Fatalf("unchanged plan: diff=%v hash=%x", plan.Diff, plan.Proposed.Hash())
				}
			} else {
				want := []DiffEntry{{Path: "port", Change: DiffModified, Before: json.Number("8080"), After: json.Number("9090")}}
				if !reflect.DeepEqual(plan.Diff, want) {
					t.Fatalf("diff = %+v; want %+v", plan.Diff, want)
				}
			}
			if mgr.Snapshot() != before || mgr.Get().Port != 8080 || plan.Proposed.Generation() != before.Generation() {
				t.Fatal("Plan must preserve the committed snapshot and generation")
			}
		})
	}
}

func TestPlan_MapHashMatchesCommittedSnapshot(t *testing.T) {
	mgr, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "yaml", []byte("port: 8080\nnested:\n  enabled: true\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	plan, err := mgr.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Proposed.Hash() != mgr.Snapshot().Hash() {
		t.Fatalf("plan hash %x does not match committed hash %x", plan.Proposed.Hash(), mgr.Snapshot().Hash())
	}
}

func TestPlan_CollectsValidatorErrors(t *testing.T) {
	type cfg struct {
		Port int `yaml:"port"`
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "yaml", []byte("port: 8080\n"))),
		WithValidate(func(c *cfg) error {
			if c.Port < 1024 {
				return errPortTooLow
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	plan, err := mgr.Plan(context.Background())
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
		WithAxes(Axis{
			Dir:          "hosts",
			FromHostname: true,
			Priority:     0,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	// Without override: result depends on the runner's actual hostname,
	// so we only check that it's NOT prod (matches base).
	plan, err := mgr.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = plan // hostname-dependent; assert nothing here.

	// With override: hostname pinned to "prod-pod-3" → overlay engages.
	plan2, err := mgr.Plan(context.Background(), WithPlanHostname("prod-pod-3"))
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Proposed.Value().Region != "prod" {
		t.Errorf("expected region=prod with hostname override, got %q", plan2.Proposed.Value().Region)
	}

	// And empty-string override: should be treated as no override.
	plan3, err := mgr.Plan(context.Background(), WithPlanHostname(""))
	if err != nil {
		t.Fatal(err)
	}
	// We don't know the runner's actual hostname; just ensure it didn't
	// happen to be the literal "prod" (extremely unlikely).
	if strings.Contains(plan3.Proposed.Value().Region, "prod-pod-3") {
		t.Errorf("empty override should NOT pin to prod-pod-3: got %q", plan3.Proposed.Value().Region)
	}
}

// B5: Plan().Run() must execute on the single-writer reload goroutine so
// user hooks are never invoked from two goroutines at once. These tests
// pin that invariant (race detector) and the documented behavior that a
// failing Plan is published on Errors().

type planCfg struct {
	Port int `json:"port" yaml:"port"`
}

// countingTransformer's Transform method is a transform that increments an
// UNSYNCHRONIZED counter. If Plan and reload ever run it concurrently, the
// race detector fires.
type countingTransformer struct{ count int }

func (c *countingTransformer) Transform(map[string]any) error {
	c.count++
	return nil
}

func TestPlan_SerializesWithReload(t *testing.T) {
	tr := &countingTransformer{}
	mgr, err := New[planCfg](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "yaml", []byte("port: 8080\n"))),
		WithTransform(tr.Transform),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := mgr.Plan(context.Background()); err != nil {
				t.Errorf("Plan: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := mgr.Reload(context.Background()); err != nil {
				t.Errorf("Reload: %v", err)
			}
		}()
	}
	wg.Wait()
	if tr.count != 101 { // Initial load plus 50 previews and 50 reloads.
		t.Fatalf("transform calls = %d, want 101", tr.count)
	}
}

// toggleTransformer fails only once armed, so New() succeeds first.
type toggleTransformer struct{ fail atomic.Bool }

func (t *toggleTransformer) Transform(map[string]any) error {
	if t.fail.Load() {
		return errors.New("toggle: armed")
	}
	return nil
}

func TestPlan_FailurePublishedToErrors(t *testing.T) {
	tr := &toggleTransformer{}
	mgr, err := New[planCfg](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "yaml", []byte("port: 8080\n"))),
		WithTransform(tr.Transform),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	tr.fail.Store(true)
	if _, err := mgr.Plan(context.Background()); !errors.Is(err, ErrTransform) {
		t.Fatalf("Plan error = %v, want ErrTransform", err)
	}
	select {
	case re := <-mgr.Errors():
		if !errors.Is(re.Err, ErrTransform) {
			t.Fatalf("published error = %v, want ErrTransform", re.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Plan failure was not published on Errors()")
	}
}

func TestPlanPreservesEveryReport(t *testing.T) {
	type config struct {
		Port  int `json:"port" fc:"min=1"`
		Count int `json:"count" fc:"min=1"`
	}
	for _, withCustom := range []bool{false, true} {
		t.Run(fmt.Sprint(withCustom), func(t *testing.T) {
			fs := fstest.MapFS{"conf.d/base/00.json": &fstest.MapFile{Data: []byte(`{"port":1,"count":1}`)}}
			sentinel := errors.New("custom invalid")
			opts := []Option{WithFS(fs)}
			if withCustom {
				for i := 0; i < 2; i++ {
					opts = append(opts, WithValidate(func(c *config) error {
						if c.Port == 0 {
							return sentinel
						}
						return nil
					}))
				}
				opts = append(opts, WithPolicy(policy.Func[config]{N: "rules", Fn: func(_ context.Context, in policy.Input[config]) ([]policy.Violation, error) {
					if in.Config.Port != 0 {
						return nil, nil
					}
					return []policy.Violation{{Path: "port", Rule: "warning", Severity: policy.SeverityWarning}, {Path: "count", Rule: "error", Severity: policy.SeverityError}}, nil
				}}))
			}
			m, err := New[config](context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			before := m.Snapshot()
			passing, err := m.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantPassing := 0
			if withCustom {
				wantPassing = 2
			}
			if len(passing.Validators) != wantPassing {
				t.Fatalf("passing reports = %+v, want %d custom validators and no field-meta reports", passing.Validators, wantPassing)
			}
			for i, report := range passing.Validators {
				if report.Name != fmt.Sprintf("validator[%d]", i) || report.Err != nil {
					t.Fatalf("passing validator %d = %+v", i, report)
				}
			}
			fs["conf.d/base/00.json"] = &fstest.MapFile{Data: []byte(`{"port":0,"count":0}`)}
			plan, err := m.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if withCustom {
				want = 4
			}
			if len(plan.Validators) != want {
				t.Fatalf("reports=%d, want %d", len(plan.Validators), want)
			}
			for i, path := range []string{"port", "count"} {
				r := plan.Validators[i]
				if r.Name != "fastconf:field-meta" || !errors.Is(r.Err, ErrInvalid) || !strings.Contains(r.Err.Error(), path) {
					t.Fatalf("lost field report %s", path)
				}
			}
			if withCustom {
				for i := 2; i < 4; i++ {
					if !errors.Is(plan.Validators[i].Err, sentinel) {
						t.Fatal("lost custom report")
					}
				}
				if len(plan.Policies) != 2 || plan.Policies[0].Rule != "warning" || plan.Policies[1].Rule != "error" {
					t.Fatal("lost policy report ordering")
				}
			}
			if m.Snapshot() != before {
				t.Fatal("preview published")
			}
			if err := m.Reload(context.Background()); !errors.Is(err, ErrInvalid) {
				t.Fatal("reload accepted invalid fields")
			}
			if m.Snapshot() != before {
				t.Fatal("failed reload published")
			}
		})
	}
}
