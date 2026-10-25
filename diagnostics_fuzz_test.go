package fastconf

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
)

// Bound both input size and container depth; the oracle only marks the
// locations it owns, so arbitrary public input cannot create false leaks.
func FuzzDiagnosticViews(f *testing.F) {
	for _, seed := range []string{`null`, `{}`, `{"a.b":{"a":{"b":[]}}}`, `[null,{},[],9007199254740993]`} {
		f.Add([]byte(seed))
	}
	type item struct {
		Token string `json:"token" fc:"secret"`
	}
	type config struct {
		Public  any            `json:"public"`
		Token   string         `json:"token" fc:"secret"`
		Items   []item         `json:"items"`
		Dynamic map[string]any `json:"dynamic"`
	}
	paths := []string{"dynamic.*.token"}
	plan := secret.Compile(reflect.TypeFor[config](), paths)
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 4096 {
			t.Skip()
		}
		dec := json.NewDecoder(bytes.NewReader(input))
		dec.UseNumber()
		var public any
		if err := dec.Decode(&public); err != nil || !diagnosticDepth(public, 16) {
			t.Skip()
		}
		const canary = "diagnostic-secret-canary"
		cfg := config{Public: public, Token: canary, Items: []item{{canary}},
			Dynamic: map[string]any{"a.b": map[string]any{"token": canary}}}
		origins := provenance.NewIndex(provenance.Full)
		origins.RecordValue("dynamic.a.b.token", SourceRef{}, canary)
		s := &State[config]{value: &cfg, secrets: paths, redactionPlan: plan, origins: origins}
		before, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		view := s.Map()
		if view["token"] == canary || view["items"].([]any)[0].(map[string]any)["token"] == canary ||
			view["dynamic"].(map[string]any)["a.b"].(map[string]any)["token"] == canary {
			t.Fatal("secret escaped its marked location")
		}
		if got := s.Explain("dynamic.a.b.token"); len(got) != 1 || got[0].Value == canary {
			t.Fatalf("unsafe origin: %+v", got)
		}
		first, err := s.Dump(JSON)
		if err != nil {
			t.Fatal(err)
		}
		mutateDiagnosticView(view["public"])
		view["dynamic"].(map[string]any)["a.b"].(map[string]any)["token"] = "caller mutation"
		view["items"].([]any)[0].(map[string]any)["token"] = "caller mutation"
		second, err := s.Dump(JSON)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("unstable view: %v", err)
		}
		next := cfg
		next.Token = canary + "-changed"
		other := &State[config]{value: &next, redactionPlan: plan, secrets: paths}
		diff := s.Diff(other)
		if len(diff) != 1 || diff[0].Path != "token" || diff[0].Change != DiffModified ||
			strings.Contains(diff[0].Before.(string), canary) || strings.Contains(diff[0].After.(string), canary) {
			t.Fatalf("secret-only diff: %+v", diff)
		}
		after, err := json.Marshal(cfg)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("diagnostics mutated input")
		}
		plain := s.Unredacted().Map()
		if plain["token"] != canary {
			t.Fatal("plaintext view lost secret")
		}
	})
}

func diagnosticDepth(value any, remaining int) bool {
	if remaining < 0 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			if !diagnosticDepth(child, remaining-1) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !diagnosticDepth(child, remaining-1) {
				return false
			}
		}
	}
	return true
}

func mutateDiagnosticView(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			mutateDiagnosticView(child)
			v[key] = "caller mutation"
		}
	case []any:
		for i, child := range v {
			mutateDiagnosticView(child)
			v[i] = "caller mutation"
		}
	}
}
