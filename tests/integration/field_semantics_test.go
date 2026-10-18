package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

func TestUnknownFields_BridgesAndFailedReload(t *testing.T) {
	type config struct {
		Port int `json:"port" yaml:"port"`
	}
	for _, format := range []fastconf.Format{fastconf.JSON, fastconf.YAML} {
		for _, mode := range []fastconf.UnknownFields{fastconf.UnknownWarn, fastconf.UnknownError, fastconf.UnknownIgnore} {
			t.Run(fmt.Sprintf("%s/%d", format, mode), func(t *testing.T) {
				var logs bytes.Buffer
				files := fstest.MapFS{"conf.d/base/app.yaml": {Data: []byte("port: 8080\n")}}
				m, err := fastconf.New[config](context.Background(), fastconf.WithFS(files),
					fastconf.WithDecodeFormat(format), fastconf.WithUnknownFields(mode),
					fastconf.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = m.Close() }()
				before := m.Snapshot()
				files["conf.d/base/app.yaml"].Data = []byte("port: 9090\nprot: 1\n")
				err = m.Reload(context.Background())
				if mode == fastconf.UnknownError {
					if !errors.Is(err, fastconf.ErrDecode) || m.Snapshot() != before {
						t.Fatalf("strict reload: err=%v preserved=%v", err, m.Snapshot() == before)
					}
				} else if err != nil || m.Get().Port != 9090 {
					t.Fatalf("reload: value=%v err=%v", m.Get(), err)
				}
				warned := strings.Contains(logs.String(), "configuration key has no matching field")
				if warned != (mode == fastconf.UnknownWarn) {
					t.Fatalf("warning mode %d: %s", mode, &logs)
				}
				// UnknownWarn must not turn a real type error into a successful load.
				before = m.Snapshot()
				files["conf.d/base/app.yaml"].Data = []byte("prot: 1\nport: [invalid]\n")
				if err := m.Reload(context.Background()); err == nil || m.Snapshot() != before {
					t.Fatalf("mixed error must preserve snapshot: %v", err)
				}
			})
		}
	}
}

type scalarDefaults struct {
	Enabled bool   `json:"enabled" yaml:"enabled" fc:"default=true"`
	Retries int    `json:"retries" yaml:"retries" fc:"default=3"`
	Label   string `json:"label" yaml:"label" fc:"default=default"`
}

type scalarValues struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Retries int    `json:"retries" yaml:"retries"`
	Label   string `json:"label" yaml:"label"`
}

func TestFieldDefaults_CompatibilityAndBaseLayers(t *testing.T) {
	for _, format := range []fastconf.Format{fastconf.JSON, fastconf.YAML} {
		for _, tc := range []struct {
			name, input string
			want        scalarValues
			null        bool
		}{
			{"missing", "{}", scalarValues{true, 3, "default"}, false},
			{"false", "enabled: false", scalarValues{false, 3, "default"}, false},
			{"zero", "retries: 0", scalarValues{true, 0, "default"}, false},
			{"empty", "label: \"\"", scalarValues{true, 3, ""}, false},
			{"null", "enabled: null\nretries: null\nlabel: null", scalarValues{}, true},
		} {
			t.Run(string(format)+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				files := fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte(tc.input)}}
				legacy, err := fastconf.Load[scalarDefaults](ctx, fastconf.WithFS(files), fastconf.WithDecodeFormat(format))
				if err != nil {
					t.Fatal(err)
				}
				if got := *legacy.Value(); got != (scalarDefaults{true, 3, "default"}) {
					t.Fatalf("legacy: %+v", got)
				}
				files["conf.d/base/a.yaml"].Data = []byte("enabled: true\nretries: 3\nlabel: default")
				files["conf.d/overlays/prod/a.yaml"] = &fstest.MapFile{Data: []byte(tc.input)}
				opts := []fastconf.Option{fastconf.WithFS(files), fastconf.WithDecodeFormat(format),
					fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}), fastconf.WithUnknownFields(fastconf.UnknownError)}
				state, err := fastconf.Load[scalarValues](ctx, opts...)
				if err != nil || *state.Value() != tc.want {
					t.Fatalf("base layers: state=%v err=%v want=%+v", state, err, tc.want)
				}
				opts = append(opts, fastconf.WithTransform(func(tree map[string]any) error {
					for _, key := range []string{"enabled", "retries", "label"} {
						if v, ok := tree[key]; ok && v == nil {
							return fmt.Errorf("%s must not be null", key)
						}
					}
					return nil
				}))
				_, err = fastconf.Load[scalarValues](ctx, opts...)
				if (err != nil) != tc.null {
					t.Fatalf("null policy: %v", err)
				}
			})
		}
		t.Run(string(format)+"/nested-multiple-layers", func(t *testing.T) {
			type config struct {
				Feature scalarValues `json:"feature" yaml:"feature"`
			}
			files := fstest.MapFS{
				"conf.d/base/a.yaml":          {Data: []byte("feature: {enabled: true, retries: 3, label: default}")},
				"conf.d/overlays/prod/a.yaml": {Data: []byte("feature: {enabled: false, retries: 0}")},
				"conf.d/overlays/prod/b.yaml": {Data: []byte("feature: {label: ''}")},
			}
			s, err := fastconf.Load[config](context.Background(), fastconf.WithFS(files), fastconf.WithDecodeFormat(format),
				fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}))
			if err != nil || s.Value().Feature != (scalarValues{}) {
				t.Fatalf("nested values: %v err=%v", s, err)
			}
		})
	}
}
