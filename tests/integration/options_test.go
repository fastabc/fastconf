package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/testutil"
	"github.com/fastabc/fastconf/providers/source"
)

// TestWithProfile_NamesEnvDefault verifies: one Profile struct with
// explicit names, an env fallback and a default, no Single/Multi exclusion.
func TestWithProfile_NamesEnvDefault(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":           {Data: []byte("port: 1\n")},
		"conf.d/overlays/prod/00.yaml":  {Data: []byte("port: 2\n")},
		"conf.d/overlays/stage/00.yaml": {Data: []byte("port: 3\n")},
	}
	load := func(p fastconf.Profile) int {
		t.Helper()
		s, err := fastconf.Load[portCfg](context.Background(), fastconf.WithFS(fs), fastconf.WithProfile(p))
		if err != nil {
			t.Fatal(err)
		}
		return s.Value().Port
	}
	if got := load(fastconf.Profile{Names: []string{"prod"}}); got != 2 {
		t.Fatalf("Names=[prod]: port %d; want 2", got)
	}
	t.Setenv("V1_PROFILE", "stage")
	if got := load(fastconf.Profile{Env: "V1_PROFILE"}); got != 3 {
		t.Fatalf("Env=V1_PROFILE: port %d; want 3", got)
	}
	t.Setenv("V1_PROFILE", "")
	if got := load(fastconf.Profile{Env: "V1_PROFILE", Default: "prod"}); got != 2 {
		t.Fatalf("Default=prod: port %d; want 2", got)
	}
}

// TestWithAxes_RelativePriority verifies: Axis.Priority orders axes
// among themselves; callers no longer pass internal band values.
func TestWithAxes_RelativePriority(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":         {Data: []byte("port: 0\n")},
		"conf.d/hosts/h1/00.yaml":     {Data: []byte("port: 2\n")},
		"conf.d/regions/eu/00.yaml":   {Data: []byte("port: 1\n")},
		"conf.d/overlays/prod/0.yaml": {Data: []byte("port: 9\n")},
	}
	t.Setenv("V1_HOST", "h1")
	t.Setenv("V1_REGION", "eu")
	s, err := fastconf.Load[portCfg](context.Background(), fastconf.WithFS(fs),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
		fastconf.WithAxes(
			fastconf.Axis{Dir: "hosts", Env: "V1_HOST", Priority: 2},
			fastconf.Axis{Dir: "regions", Env: "V1_REGION", Priority: 1},
		))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Value().Port; got != 2 {
		t.Fatalf("port = %d; want 2 (hosts axis above regions axis above the prod overlay)", got)
	}
}

// TestWithProvider_VariadicTieBreaksByDeclaration verifies:
// WithProvider takes several providers; equal priorities merge in
// declaration order.
func TestWithProvider_VariadicTieBreaksByDeclaration(t *testing.T) {
	a := source.NewBytes("a", "yaml", []byte("port: 1\n")).WithPriority(contracts.PriorityKV)
	b := source.NewBytes("b", "yaml", []byte("port: 2\n")).WithPriority(contracts.PriorityKV)
	s, err := fastconf.Load[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Value().Port; got != 2 {
		t.Fatalf("port = %d; want 2 from the later equal-priority provider", got)
	}
}

// TestWithTransform_FuncsRunInOrder verifies: transforms are plain
// functions over the merged map, run in declaration order before decode.
func TestWithTransform_FuncsRunInOrder(t *testing.T) {
	var order []string
	s, err := fastconf.Load[portCfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte("port: 1\n")}}),
		fastconf.WithTransform(
			func(m map[string]any) error { order = append(order, "a"); m["port"] = 5; return nil },
			func(m map[string]any) error { order = append(order, "b"); return nil },
		))
	if err != nil {
		t.Fatal(err)
	}
	if s.Value().Port != 5 || !slices.Equal(order, []string{"a", "b"}) {
		t.Fatalf("port=%d order=%v; want 5 [a b]", s.Value().Port, order)
	}
}

// TestWithValidate_BlocksInvalid verifies that a later validator can reject
// a configuration accepted by an earlier validator.
func TestWithValidate_BlocksInvalid(t *testing.T) {
	_, err := fastconf.Load[portCfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte("port: 0\n")}}),
		fastconf.WithValidate(
			func(*portCfg) error { return nil },
			func(c *portCfg) error {
				if c.Port == 0 {
					return errors.New("port required")
				}
				return nil
			},
		))
	if err == nil || !strings.Contains(err.Error(), "port required") {
		t.Fatalf("err = %v; want the second validator's failure", err)
	}
}

func TestWithDecodeFormat(t *testing.T) {
	type cfg struct {
		JSON int `json:"json_value" yaml:"json_value"`
		YAML int `yaml:"yaml_value"`
	}
	fs := fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte("json_value: 7\nyaml_value: 9\n")}}
	for _, tc := range []struct {
		name string
		opts []fastconf.Option
		want cfg
	}{
		{"default JSON", nil, cfg{JSON: 7}},
		{"YAML", []fastconf.Option{fastconf.WithDecodeFormat(fastconf.YAML)}, cfg{JSON: 7, YAML: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := append([]fastconf.Option{fastconf.WithFS(fs)}, tc.opts...)
			s, err := fastconf.Load[cfg](context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := *s.Value(); got != tc.want {
				t.Fatalf("decoded = %+v; want %+v", got, tc.want)
			}
		})
	}
	t.Run("unsupported TOML bridge", func(t *testing.T) {
		if _, err := fastconf.Load[cfg](context.Background(), fastconf.WithFS(fs), fastconf.WithDecodeFormat(fastconf.TOML)); err == nil {
			t.Fatal("WithDecodeFormat(TOML) must be rejected")
		}
	})
}

func TestWithDecodeFormat_YAMLFromJSONNumbers(t *testing.T) {
	type cfg struct {
		Count uint64             `yaml:"count"`
		Ratio float64            `yaml:"ratio"`
		Items []int              `yaml:"items"`
		Rates map[string]float64 `yaml:"rates"`
	}
	fs := fstest.MapFS{"conf.d/base/0.json": {Data: []byte(`{"count":18446744073709551615,"ratio":1.25,"items":[-2,3],"rates":{"true":1e2}}`)}}
	m, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fs), fastconf.WithDecodeFormat(fastconf.YAML))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	got := m.Get()
	if got.Count != ^uint64(0) || got.Ratio != 1.25 || len(got.Items) != 2 || got.Items[0] != -2 || got.Items[1] != 3 || got.Rates["true"] != 100 {
		t.Fatalf("decoded numbers = %+v", got)
	}
	if err := m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"count": uint64(42), "ratio": 2.5})); err != nil {
		t.Fatal(err)
	}
	if got := m.Get(); got.Count != 42 || got.Ratio != 2.5 {
		t.Fatalf("decoded override = %+v", got)
	}
}

func TestWithDecodeFormat_PreservesProviderNullContainers(t *testing.T) {
	for _, format := range []fastconf.Format{fastconf.JSON, fastconf.YAML} {
		t.Run(format.String(), func(t *testing.T) {
			p := testutil.NewFakeProvider("nullable", contracts.PriorityKV, map[string]any{
				"nil_map": map[string]any(nil), "empty_map": map[string]any{},
				"nil_slice": []any(nil), "empty_slice": []any{},
			})
			s, err := fastconf.Load[map[string]any](context.Background(), fastconf.WithFS(fstest.MapFS{}),
				fastconf.WithProvider(p), fastconf.WithDecodeFormat(format))
			if err != nil {
				t.Fatal(err)
			}
			out, err := s.Unredacted().Dump(fastconf.JSON)
			if err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, out); err != nil {
				t.Fatal(err)
			}
			if got := compact.String(); got != `{"empty_map":{},"empty_slice":[],"nil_map":null,"nil_slice":null}` {
				t.Fatalf("provider containers changed: %s", got)
			}
		})
	}
}

func TestWithStrictMerge_TypeConflictFails(t *testing.T) {
	for _, tc := range []struct{ name, base, overlay string }{
		{"scalar to map", "port: 1\n", "port: {nested: true}\n"},
		{"map to scalar", "server: {addr: ':80'}\n", "server: oops-string\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fstest.MapFS{
				"conf.d/base/00.yaml": {Data: []byte(tc.base)},
				"conf.d/base/10.yaml": {Data: []byte(tc.overlay)},
			}
			_, err := fastconf.Load[map[string]any](context.Background(), fastconf.WithFS(fs), fastconf.WithStrictMerge(true))
			if !errors.Is(err, fastconf.ErrMerge) {
				t.Fatalf("strict merge error = %v; want ErrMerge", err)
			}
		})
	}
}

// TestWithUnknownFields verifies: a misspelled key fails under Error,
// is logged under Warn (the default) and is silent under Ignore.
func TestWithUnknownFields(t *testing.T) {
	fs := fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte("port: 1\nprot: 2\n")}}
	if _, err := fastconf.Load[portCfg](context.Background(), fastconf.WithFS(fs),
		fastconf.WithUnknownFields(fastconf.UnknownError)); !errors.Is(err, fastconf.ErrDecode) || !strings.Contains(err.Error(), "prot") {
		t.Fatalf("Error mode: err = %v; want ErrDecode naming the key", err)
	}
	for _, tc := range []struct {
		name  string
		opts  []fastconf.Option
		warns bool
	}{
		{"default warns", nil, true},
		{"ignore", []fastconf.Option{fastconf.WithUnknownFields(fastconf.UnknownIgnore)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			opts := append([]fastconf.Option{fastconf.WithFS(fs), fastconf.WithLogger(slog.New(slog.NewTextHandler(&buf, nil)))}, tc.opts...)
			s, err := fastconf.Load[portCfg](context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			if s.Value().Port != 1 {
				t.Fatalf("port = %d; want 1", s.Value().Port)
			}
			if got := strings.Contains(buf.String(), "prot"); got != tc.warns {
				t.Fatalf("warning logged = %v; want %v (log: %s)", got, tc.warns, buf.String())
			}
		})
	}
}

func TestOptions_NilEntries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		option   fastconf.Option
		rejected bool
	}{
		{"provider", fastconf.WithProvider(nil), false},
		{"generator", fastconf.WithGenerator(nil), false},
		{"typed hook", fastconf.WithTypedHook(nil), false},
		{"validator", fastconf.WithValidate[portCfg](nil), false},
		{"transform", fastconf.WithTransform(nil), true},
		{"logger", fastconf.WithLogger(nil), true},
		{"tracer", fastconf.WithTracer(nil), true},
		{"observer", fastconf.WithObserver(nil), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := fastconf.Load[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("port: 8080\n")}}), tc.option)
			if tc.rejected {
				if s != nil || !errors.Is(err, fastconf.ErrFastConf) {
					t.Fatalf("Load = %v, %v; want nil, ErrFastConf", s, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Value().Port != 8080 {
				t.Fatalf("port = %d", s.Value().Port)
			}
		})
	}
}

func TestWithValidate_Result(t *testing.T) {
	rejected := errors.New("port rejected")
	for _, tc := range []struct {
		name     string
		validate func(*portCfg) error
		want     error
	}{
		{"accepted", func(c *portCfg) error {
			if c.Port != 8080 {
				t.Errorf("validator saw port %d", c.Port)
			}
			return nil
		}, nil},
		{"rejected", func(*portCfg) error { return rejected }, rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := fastconf.Load[portCfg](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte("port: 8080\n")}}), fastconf.WithValidate(tc.validate))
			if tc.want != nil {
				if s != nil || !errors.Is(err, tc.want) || !errors.Is(err, fastconf.ErrInvalid) {
					t.Fatalf("Load = %v, %v; want nil and classified validation cause", s, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Value().Port != 8080 {
				t.Fatalf("port = %d", s.Value().Port)
			}
		})
	}
}

// testEnvVar is a unique env var name used in overlay-axis hostname tests to
// avoid accidentally interfering with real HOST, REGION, or ZONE variables.
const testHostAxisEnv = "FASTCONF_TEST_HOST_AXIS_VAR"

// unsetEnvForTest ensures the given env var is absent for the duration of the
// test, restoring the original value (or absence) on cleanup.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	if orig, present := os.LookupEnv(key); present {
		t.Cleanup(func() { _ = os.Setenv(key, orig) })
		_ = os.Unsetenv(key)
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(key) })
	}
}

// TestOverlayAxis_FromHostname_UsesHostname verifies that when the env
// var is absent and FromHostname is true, os.Hostname() is used as the
// axis value and the matching overlay directory is loaded.
func TestOverlayAxis_FromHostname_UsesHostname(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	unsetEnvForTest(t, testHostAxisEnv)

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithAxes(
			fastconf.Axis{
				Dir:          "hosts",
				Env:          testHostAxisEnv,
				Priority:     200,
				FromHostname: true,
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if got := mgr.Get().Name; got != "from-hostname" {
		t.Errorf("name = %q; want %q (hostname overlay should be loaded)", got, "from-hostname")
	}
}

// TestOverlayAxis_FromHostname_EnvVarWins verifies that an explicit
// non-empty env var value takes precedence over os.Hostname().
func TestOverlayAxis_FromHostname_EnvVarWins(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
		"conf.d/hosts/explicit-host/00.yaml":    {Data: []byte("name: from-explicit\n")},
	}

	t.Setenv(testHostAxisEnv, "explicit-host")

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithAxes(
			fastconf.Axis{
				Dir:          "hosts",
				Env:          testHostAxisEnv,
				Priority:     200,
				FromHostname: true,
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if got := mgr.Get().Name; got != "from-explicit" {
		t.Errorf("name = %q; want %q (explicit env var should win over hostname)", got, "from-explicit")
	}
}

// TestOverlayAxis_FromHostname_EmptyEnvVarSkips verifies that when the
// env var is explicitly set to an empty string, the axis is skipped even when
// FromHostname is true. This is the operator opt-out path.
func TestOverlayAxis_FromHostname_EmptyEnvVarSkips(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	// Set env var to empty string to disable the axis.
	t.Setenv(testHostAxisEnv, "")

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithAxes(
			fastconf.Axis{
				Dir:          "hosts",
				Env:          testHostAxisEnv,
				Priority:     200,
				FromHostname: true,
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if got := mgr.Get().Name; got != "base" {
		t.Errorf("name = %q; want %q (empty env var should disable host overlay)", got, "base")
	}
}

// TestOverlayAxis_FromHostname_FalseSkips verifies the default behavior
// (FromHostname: false): when the env var is absent, the axis is skipped.
func TestOverlayAxis_FromHostname_FalseSkips(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	unsetEnvForTest(t, testHostAxisEnv)

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithAxes(
			fastconf.Axis{
				Dir:          "hosts",
				Env:          testHostAxisEnv,
				Priority:     200,
				FromHostname: false, // explicit false
			},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if got := mgr.Get().Name; got != "base" {
		t.Errorf("name = %q; want %q (no hostname fallback when FromHostname is false)", got, "base")
	}
}

// TestAxis_HostsFromHostname verifies that a hosts axis with FromHostname
// falls back to os.Hostname().
func TestAxis_HostsFromHostname(t *testing.T) {
	// Access internal options via a white-box test approach: verify preset behavior
	// by testing the observable outcome rather than the struct field directly.
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname() failed, skipping:", err)
	}

	type cfg struct {
		Name string `yaml:"name"`
	}

	// Ensure the default HOST env var is absent so hostname fallback can activate.
	unsetEnvForTest(t, "HOST")

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                   {Data: []byte("name: base\n")},
		"conf.d/hosts/" + hostname + "/00.yaml": {Data: []byte("name: from-hostname\n")},
	}

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithAxes(fastconf.Axis{Dir: "hosts", Env: "HOST", Priority: 2, FromHostname: true}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if got := mgr.Get().Name; got != "from-hostname" {
		t.Errorf("name = %q; want %q (hosts axis should use hostname fallback)", got, "from-hostname")
	}
}

// TestOverlayAxis_PriorityOrdersMergeNotDeclaration verifies that AxisSpec.Priority
// decides merge order even when the higher-priority axis is declared first.
func TestOverlayAxis_PriorityOrdersMergeNotDeclaration(t *testing.T) {
	type cfg struct {
		Port int `yaml:"port"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":            {Data: []byte("port: 0\n")},
		"conf.d/hosts/h1/00.yaml":        {Data: []byte("port: 2\n")},
		"conf.d/regions/eu-west/00.yaml": {Data: []byte("port: 1\n")},
	}
	t.Setenv("FASTCONF_TEST_AXIS_HOST", "h1")
	t.Setenv("FASTCONF_TEST_AXIS_REGION", "eu-west")

	mgr, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithAxes(
			fastconf.Axis{Dir: "hosts", Env: "FASTCONF_TEST_AXIS_HOST", Priority: 200},
			fastconf.Axis{Dir: "regions", Env: "FASTCONF_TEST_AXIS_REGION", Priority: 0},
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Port; got != 2 {
		t.Fatalf("port = %d; want 2 (hosts axis priority 3200 must beat regions 3000)", got)
	}
}
