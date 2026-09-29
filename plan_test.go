package fastconf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	if before.Cause().At.IsZero() {
		t.Fatal("initial commit has no timestamp")
	}

	for _, port := range []int{8080, 9090} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("port: %d\n", port))}
			started := time.Now()
			plan, err := mgr.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Proposed == nil || plan.Proposed.Value().Port != port {
				t.Fatalf("proposed = %+v; want port %d", plan.Proposed, port)
			}
			if at := plan.Proposed.Cause().At; at.Before(started) || at.After(time.Now()) {
				t.Fatalf("preview timestamp = %v; want time of this Plan call", at)
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

type hostnameConfig struct {
	Region string `json:"region"`
}

func TestPlan_HostnameOverride(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	target := host + "-preview"
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":                 {Data: []byte("region: base\n")},
		"conf.d/hosts/" + host + "/00.yaml":   {Data: []byte("region: local\n")},
		"conf.d/hosts/" + target + "/00.yaml": {Data: []byte("region: target\n")},
	}
	mgr, err := New[hostnameConfig](context.Background(), WithFS(fs), WithAxes(Axis{Dir: "hosts", FromHostname: true}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	before := mgr.Snapshot()
	if before.Value().Region != "local" {
		t.Fatalf("initial region = %q", before.Value().Region)
	}
	for _, tc := range []struct {
		name string
		opts []PlanOption
		want string
	}{
		{"current host", nil, "local"},
		{"target host", []PlanOption{WithPlanHostname(target)}, "target"},
		{"empty override", []PlanOption{WithPlanHostname("")}, "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := mgr.Plan(context.Background(), tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := plan.Proposed.Value().Region; got != tc.want {
				t.Fatalf("region = %q; want %q", got, tc.want)
			}
			if mgr.Snapshot() != before {
				t.Fatal("preview published a snapshot")
			}
		})
	}
}

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
