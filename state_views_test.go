package fastconf_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

type safeCfg struct {
	Name string `json:"name"`
	DB   struct {
		Host     string `json:"host"`
		Password string `json:"password" fc:"secret"`
	} `json:"db"`
}

func newSafeManager(t *testing.T, body string) *fastconf.Manager[safeCfg] {
	t.Helper()
	fs := fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte(body)}}
	m, err := fastconf.New[safeCfg](context.Background(), fastconf.WithFS(fs),
		fastconf.WithProvenance(fastconf.ProvenanceFull))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestState_ViewsRedactByDefault verifies: every map/text view of a
// snapshot masks secrets unless the caller asks for Unredacted().
func TestState_ViewsRedactByDefault(t *testing.T) {
	m := newSafeManager(t, "name: app\ndb:\n  host: h\n  password: hunter2\n")
	s := m.Snapshot()

	db := s.Map()["db"].(map[string]any)
	if db["password"] == "hunter2" || db["host"] != "h" {
		t.Fatalf("Map() = %v; want password masked, host kept", db)
	}
	out, err := s.Dump(fastconf.YAML)
	if err != nil || strings.Contains(string(out), "hunter2") {
		t.Fatalf("Dump(YAML) = %q, %v; want no plaintext secret", out, err)
	}
	for _, o := range s.Explain("db.password") {
		if o.Value == "hunter2" {
			t.Fatalf("Explain leaked the secret: %+v", o)
		}
	}
	if got := s.Unredacted().Map()["db"].(map[string]any)["password"]; got != "hunter2" {
		t.Fatalf("Unredacted().Map() password = %v; want plaintext", got)
	}
	plain, err := s.Unredacted().Dump(fastconf.JSON)
	if err != nil || !strings.Contains(string(plain), "hunter2") {
		t.Fatalf("Unredacted().Dump = %q, %v; want plaintext", plain, err)
	}
}

// TestState_DiffDetectsSecretChangesWithoutLeaking verifies: Diff
// decides changes on plaintext but shows masked values.
func TestState_DiffDetectsSecretChangesWithoutLeaking(t *testing.T) {
	m := newSafeManager(t, "db:\n  password: old\n")
	before := m.Snapshot()
	if err := m.Reload(context.Background(), fastconf.WithOverride(map[string]any{"db": map[string]any{"password": "new"}})); err != nil {
		t.Fatal(err)
	}
	diff := before.Diff(m.Snapshot())
	if len(diff) != 1 || diff[0].Path != "db.password" {
		t.Fatalf("Diff = %+v; want one db.password change", diff)
	}
	if diff[0].Before == "old" || diff[0].After == "new" {
		t.Fatalf("Diff leaked secret values: %+v", diff[0])
	}
}

func TestState_ExplainMasksHistoricalSecrets(t *testing.T) {
	type credential struct {
		Password string `json:"password" fc:"secret"`
	}
	type config struct {
		Items    []credential     `json:"items,omitempty"`
		Password string           `json:"password,omitempty" fc:"secret"`
		Tokens   []map[string]any `json:"tokens,omitempty"`
	}
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprint(removed), func(t *testing.T) {
			opts := []fastconf.Option{
				fastconf.WithFS(fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte("items: [{password: private-list}]\npassword: private-field\ntokens: [{token: private-pattern}]\n")}}),
				fastconf.WithProvenance(fastconf.ProvenanceFull),
				fastconf.WithSecretPaths("tokens.*.token"),
			}
			if removed {
				opts = append(opts, fastconf.WithTransform(func(m map[string]any) error {
					clear(m)
					return nil
				}))
			}
			s, err := fastconf.Load[config](context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"items", "password", "tokens"} {
				origins := s.Explain(path)
				if len(origins) != 1 || strings.Contains(fmt.Sprint(origins), "private-") {
					t.Errorf("Explain(%q) leaked historical secrets: %+v", path, origins)
				}
			}
		})
	}
}

func TestState_ExplainMasksInputAliases(t *testing.T) {
	type config struct {
		Password string `json:"password" yaml:"credential" fc:"secret"`
	}
	for _, tc := range []struct {
		key     string
		decoder fastconf.Format
	}{{"PASSWORD", fastconf.JSON}, {"credential", fastconf.YAML}} {
		t.Run(tc.key, func(t *testing.T) {
			s, err := fastconf.Load[config](context.Background(),
				fastconf.WithFS(fstest.MapFS{"conf.d/base/0.yaml": {Data: []byte(tc.key + ": private-alias")}}),
				fastconf.WithDecoder(tc.decoder), fastconf.WithProvenance(fastconf.ProvenanceFull))
			if err != nil {
				t.Fatal(err)
			}
			if s.Value().Password != "private-alias" {
				t.Fatal("input alias was not decoded")
			}
			if strings.Contains(fmt.Sprint(s.Explain(tc.key)), "private-alias") {
				t.Fatal("input alias leaked through provenance")
			}
		})
	}
}
