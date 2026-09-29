package fastconf

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/coalesce"
	"github.com/fastabc/fastconf/providers/source"
)

// TestWithWatch_FieldOverrides exercises the Watch fields and the
// override rule — non-zero timings win over the Profile preset.
func TestWithWatch_FieldOverrides(t *testing.T) {
	t.Run("Watch enables file watching", func(t *testing.T) {
		got := applyOpts(WithWatch(Watch{}))
		if !got.Watch {
			t.Error("Watch flag not set")
		}
	})
	t.Run("Paths appends WatchPaths", func(t *testing.T) {
		got := applyOpts(WithWatch(Watch{Paths: []string{"/etc/foo", "/etc/bar"}}))
		if len(got.WatchPaths) != 2 {
			t.Errorf("WatchPaths=%v", got.WatchPaths)
		}
	})
	t.Run("Profile applies, per-field timings override", func(t *testing.T) {
		got := applyOpts(WithWatch(Watch{Profile: ProfileLocalDev, Quiet: 25 * time.Millisecond}))
		if got.Coalesce.Quiet != 25*time.Millisecond {
			t.Errorf("per-field Quiet override lost: %v", got.Coalesce.Quiet)
		}
		if got.Coalesce.MaxLag != coalesce.ProfileLocalDev.Apply().MaxLag {
			t.Errorf("profile MaxLag not applied: %v", got.Coalesce.MaxLag)
		}
	})
	t.Run("No WithWatch leaves the watcher disabled", func(t *testing.T) {
		if got := applyOpts(); got.Watch {
			t.Error("watcher enabled without WithWatch")
		}
	})
}

// TestWithProfile_InvalidMatchNamesField proves the
// startup-time validator wraps invalid expressions with "WithProfile.Match"
// so failures point users at the option field.
func TestWithProfile_InvalidMatchNamesField(t *testing.T) {
	_, err := New[struct{}](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("{}\n")},
		}),
		WithDir("conf.d"),
		WithProfile(Profile{Match: "prod & ("}),
	)
	if err == nil {
		t.Fatal("expected expression error")
	}
	if !errors.Is(err, ErrFastConf) {
		t.Errorf("want ErrFastConf, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Errorf("profile expression syntax must not classify as ErrDecode: %v", err)
	}
	if !strings.Contains(err.Error(), "WithProfile.Match") {
		t.Errorf("error must point at option field: %v", err)
	}
}

// applyOpts collects the supplied options against a fresh options
// so individual fields can be asserted without running the full Manager
// pipeline.
func applyOpts(opts ...Option) *options {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	return o
}

type generatedConfig struct {
	App struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"app"`
}

// TestGenerator_RawLayerPriority verifies that two RawLayers emitted by
// the same Generator at distinct Priority values are stamped onto
// SourceRef with Kind=LayerGenerator and Priority offset into
// the generator band (7000). The assemble stage walks layers in
// priority-ascending order, so higher RawLayer.Priority wins on conflicting
// keys.
func TestGenerator_RawLayerPriority(t *testing.T) {
	gen := &priorityGen{}
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("k: file\n")},
	}
	mgr, err := New[map[string]any](context.Background(),
		WithFS(fs),
		WithGenerator(gen),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	// Higher RawLayer.Priority wins between the two generator layers,
	// and the generator band (7000+) overrides the file base layer
	// (1000+) regardless.
	if got := (*mgr.Get())["k"]; got != "high" {
		t.Fatalf("expected high-priority generator layer to win, got %v", got)
	}
	var lowSeen, highSeen bool
	for _, s := range mgr.Snapshot().Sources() {
		if s.Kind != LayerGenerator {
			continue
		}
		switch s.Path {
		case "gen://prio/low":
			if s.Priority != 7000+10 {
				t.Errorf("low Priority offset: got %d want %d", s.Priority, 7010)
			}
			lowSeen = true
		case "gen://prio/high":
			if s.Priority != 7000+90 {
				t.Errorf("high Priority offset: got %d want %d", s.Priority, 7090)
			}
			highSeen = true
		}
	}
	if !lowSeen || !highSeen {
		t.Fatalf("expected both generator layers reported, low=%v high=%v", lowSeen, highSeen)
	}
}

type priorityGen struct{}

func (priorityGen) Name() string { return "prio" }
func (priorityGen) Generate(_ context.Context) ([]contracts.RawLayer, error) {
	return []contracts.RawLayer{
		{Name: "low", Codec: "yaml", Data: []byte("k: low\n"), Priority: 10},
		{Name: "high", Codec: "yaml", Data: []byte("k: high\n"), Priority: 90},
	}, nil
}

// TestGenerator_PriorityOrdersMergeNotDeclaration verifies that RawLayer.Priority
// decides merge order even when the higher-priority layer is emitted first.
func TestGenerator_PriorityOrdersMergeNotDeclaration(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("k: file\n")},
	}
	mgr, err := New[map[string]any](context.Background(),
		WithFS(fs),
		WithGenerator(reversedPriorityGen{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := (*mgr.Get())["k"]; got != "high" {
		t.Fatalf("higher RawLayer.Priority must win regardless of emission order, got %v", got)
	}
}

type reversedPriorityGen struct{}

func (reversedPriorityGen) Name() string { return "reversed" }
func (reversedPriorityGen) Generate(_ context.Context) ([]contracts.RawLayer, error) {
	return []contracts.RawLayer{
		{Name: "high", Codec: "yaml", Data: []byte("k: high\n"), Priority: 90},
		{Name: "low", Codec: "yaml", Data: []byte("k: low\n"), Priority: 10},
	}, nil
}

func TestGenerator_OverlaysFiles(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
app:
  name: "fastconf"
  version: "0.0.0"
`)},
	}
	mgr, err := New[generatedConfig](context.Background(),
		WithFS(fs),
		WithGenerator(buildInfoGen{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	got := mgr.Get()
	if got.App.Name != "fastconf" {
		t.Fatalf("name should come from file: got %q", got.App.Name)
	}
	if got.App.Version != "1.2.3" {
		t.Fatalf("generator should win on version: got %q", got.App.Version)
	}
	if got.App.Commit != "abc" {
		t.Fatalf("generator should set commit: got %q", got.App.Commit)
	}
}

type buildInfoGen struct{}

func (buildInfoGen) Name() string { return "buildinfo" }

func (buildInfoGen) Generate(_ context.Context) ([]contracts.RawLayer, error) {
	return []contracts.RawLayer{{
		Name:  "info",
		Codec: "json",
		Data:  []byte(`{"app":{"version":"1.2.3","commit":"abc"}}`),
	}}, nil
}

func TestProfile_ProfilesMatchExpression(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod-eu/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod & eu\n")},
		"conf.d/overlays/prod-eu/00.yaml":    &fstest.MapFile{Data: []byte("name: prod-eu\n")},
		"conf.d/overlays/prod-us/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod & us\n")},
		"conf.d/overlays/prod-us/00.yaml":    &fstest.MapFile{Data: []byte("name: prod-us\n")},
		"conf.d/overlays/canary/_meta.yaml":  &fstest.MapFile{Data: []byte("match: canary\n")},
		"conf.d/overlays/canary/00.yaml":     &fstest.MapFile{Data: []byte("name: canary\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod", "eu"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod-eu" {
		t.Fatalf("expected prod-eu overlay to win, got %q", got)
	}
}

func TestProfile_FallbackToNameMembership(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":          &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("name: prod\n")},
		"conf.d/overlays/dev/00.yaml":  &fstest.MapFile{Data: []byte("name: dev\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("name-membership fallback failed, got %q", got)
	}
}

func TestProfile_GlobalProfileExpr(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":               &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml":      &fstest.MapFile{Data: []byte("name: prod\n")},
		"conf.d/overlays/canary/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod\n")},
		"conf.d/overlays/canary/00.yaml":    &fstest.MapFile{Data: []byte("name: canary\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod"}, Match: "!canary"}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("global expr should suppress canary, got %q", got)
	}
}

func TestProfile_SingleProfile(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":          &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("name: prod\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile(Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("single-profile path broken, got %q", got)
	}
}

func TestProfile_InvalidExprFailsAtNew(t *testing.T) {
	_, err := New[struct{}](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("inline", "yaml", []byte("{}"))),
		WithProfile(Profile{Match: "prod & ("}),
	)
	if err == nil || !strings.Contains(err.Error(), "WithProfile.Match") {
		t.Fatalf("expected WithProfile.Match error, got %v", err)
	}
	if !errors.Is(err, ErrFastConf) {
		t.Fatalf("expected ErrFastConf, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("profile expression syntax must not classify as ErrDecode: %v", err)
	}
}

func TestWithAxes_PriorityWinsOverFileCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		axes []Axis
	}{
		{"ascending", []Axis{{Dir: "region", Env: "AXIS_REGION", Priority: 0}, {Dir: "tier", Env: "AXIS_TIER", Priority: 1}}},
		{"reversed", []Axis{{Dir: "tier", Env: "AXIS_TIER", Priority: 1}, {Dir: "region", Env: "AXIS_REGION", Priority: 0}}},
		{"equal preserves declaration", []Axis{{Dir: "region", Env: "AXIS_REGION", Priority: 1}, {Dir: "tier", Env: "AXIS_TIER", Priority: 1}}},
		{"arbitrary ranks", []Axis{{Dir: "tier", Env: "AXIS_TIER", Priority: 4500}, {Dir: "region", Env: "AXIS_REGION", Priority: -10000}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AXIS_REGION", "eu")
			t.Setenv("AXIS_TIER", "gold")
			fs := fstest.MapFS{
				"conf.d/base/00.yaml":      &fstest.MapFile{Data: []byte("k: base")},
				"conf.d/region/eu/10.yaml": &fstest.MapFile{Data: []byte("k: region-10")},
				"conf.d/region/eu/20.yaml": &fstest.MapFile{Data: []byte("k: region-20")},
				"conf.d/region/eu/30.yaml": &fstest.MapFile{Data: []byte("k: region-30")},
				"conf.d/tier/gold/10.yaml": &fstest.MapFile{Data: []byte("k: tier-10")},
			}
			m, err := New[map[string]any](context.Background(), WithFS(fs), WithAxes(tc.axes...))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			if got := (*m.Get())["k"]; got != "tier-10" {
				t.Fatalf("winner = %v", got)
			}
			for _, src := range m.Snapshot().Sources() {
				if src.Priority >= 7000 {
					t.Fatalf("file escaped its band: %+v", src)
				}
			}
		})
	}
}

func TestWithAxes_RejectsBandOverflow(t *testing.T) {
	t.Setenv("AXIS_VALUE", "selected")
	axes := make([]Axis, 40)
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("k: base")}}
	for i := range axes {
		axes[i] = Axis{Dir: fmt.Sprintf("axis%02d", i), Env: "AXIS_VALUE", Priority: i}
	}
	for i := range 100 {
		fs[fmt.Sprintf("conf.d/axis39/selected/%03d.yaml", i)] = &fstest.MapFile{Data: []byte("k: axis")}
	}
	m, err := New[map[string]any](context.Background(), WithFS(fs), WithAxes(axes...))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	sources := m.Snapshot().Sources()
	if got := sources[len(sources)-1].Priority; got != 6999 {
		t.Fatalf("last file priority = %d, want 6999", got)
	}
	_, err = New[map[string]any](context.Background(), WithFS(fs), WithAxes(axes...), WithAxes(Axis{Dir: "overflow"}))
	if !errors.Is(err, ErrFastConf) || !strings.Contains(err.Error(), "WithAxes") {
		t.Fatalf("want fatal WithAxes overflow, got %v", err)
	}
}

func TestWithHistory_RejectsNegativeCapacity(t *testing.T) {
	// Reusing the same option must report the same error on every call.
	opt := WithHistory(-1)
	for range 2 {
		_, err := New[map[string]any](context.Background(), opt)
		if !errors.Is(err, ErrFastConf) || !strings.Contains(err.Error(), "WithHistory") {
			t.Fatalf("want WithHistory error, got %v", err)
		}
	}
}

type fixedGen struct {
	prio int
	body string
}

func (fixedGen) Name() string { return "fixed" }
func (g fixedGen) Generate(context.Context) ([]contracts.RawLayer, error) {
	return []contracts.RawLayer{{Name: "g", Codec: "yaml", Data: []byte(g.body), Priority: g.prio}}, nil
}

// Priorities order layers only within their class: no provider or generator
// priority crosses the file < generator < provider < override boundaries.
func TestPriority_DoesNotCrossLayerClasses(t *testing.T) {
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("v: file\n")}}
	type cfg struct {
		V string `json:"v"`
	}
	for _, tc := range []struct {
		name string
		opts []Option
		want string
	}{
		{"huge provider below override", []Option{
			WithProvider(source.NewBytes("p", "yaml", []byte("v: provider\n")).WithPriority(1 << 40)),
		}, "override"},
		{"negative generator above file", []Option{
			WithGenerator(fixedGen{prio: -1 << 40, body: "v: gen\n"}),
		}, "override"},
		{"negative provider above huge generator", []Option{
			WithGenerator(fixedGen{prio: math.MaxInt, body: "v: gen\n"}),
			WithProvider(source.NewBytes("p", "yaml", []byte("v: provider\n")).WithPriority(math.MinInt)),
		}, "provider"},
		{"negative generator above file without override", []Option{
			WithGenerator(fixedGen{prio: -1 << 40, body: "v: gen\n"}),
		}, "gen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := New[cfg](context.Background(), append([]Option{WithFS(fs)}, tc.opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = mgr.Close() }()
			if tc.want == "override" {
				if err := mgr.Reload(context.Background(), WithOverride(map[string]any{"v": "override"})); err != nil {
					t.Fatal(err)
				}
			}
			if got := mgr.Get().V; got != tc.want {
				t.Fatalf("v = %q, want %q", got, tc.want)
			}
		})
	}
}
