package fastconf_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

func TestWithDecoder(t *testing.T) {
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
		{"YAML", []fastconf.Option{fastconf.WithDecoder(fastconf.YAML)}, cfg{JSON: 7, YAML: 9}},
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
		if _, err := fastconf.Load[cfg](context.Background(), fastconf.WithFS(fs), fastconf.WithDecoder(fastconf.TOML)); err == nil {
			t.Fatal("WithDecoder(TOML) must be rejected")
		}
	})
}

func TestWithDecoder_YAMLFromJSONNumbers(t *testing.T) {
	type cfg struct {
		Count uint64             `yaml:"count"`
		Ratio float64            `yaml:"ratio"`
		Items []int              `yaml:"items"`
		Rates map[string]float64 `yaml:"rates"`
	}
	fs := fstest.MapFS{"conf.d/base/0.json": {Data: []byte(`{"count":18446744073709551615,"ratio":1.25,"items":[-2,3],"rates":{"true":1e2}}`)}}
	m, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fs), fastconf.WithDecoder(fastconf.YAML))
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

func TestWithDecoder_PreservesProviderNullContainers(t *testing.T) {
	for _, format := range []fastconf.Format{fastconf.JSON, fastconf.YAML} {
		t.Run(format.String(), func(t *testing.T) {
			p := testutil.NewFakeProvider("nullable", contracts.PriorityKV, map[string]any{
				"nil_map": map[string]any(nil), "empty_map": map[string]any{},
				"nil_slice": []any(nil), "empty_slice": []any{},
			})
			s, err := fastconf.Load[map[string]any](context.Background(), fastconf.WithFS(fstest.MapFS{}),
				fastconf.WithProvider(p), fastconf.WithDecoder(format))
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
