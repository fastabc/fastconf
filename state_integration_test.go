package fastconf_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/providers/source"
)

// TestState_MapIsFreshDeepCopy: Map returns a tree the caller may modify
// without affecting the snapshot, including nested containers.
func TestState_MapIsFreshDeepCopy(t *testing.T) {
	type cfg struct {
		Labels []any `json:"labels"`
	}
	mgr, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("seed", "yaml", []byte("{}"))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if err := mgr.Reload(context.Background(), fastconf.WithOverride(map[string]any{
		"labels": []any{"one", map[string]any{"value": "kept"}},
	})); err != nil {
		t.Fatal(err)
	}
	s := mgr.Snapshot()
	for _, view := range []func() map[string]any{s.Map, s.Unredacted().Map} {
		labels := view()["labels"].([]any)
		labels[0] = "tampered"
		labels[1].(map[string]any)["value"] = "tampered"
		again := view()["labels"].([]any)
		if again[0] != "one" || again[1].(map[string]any)["value"] != "kept" {
			t.Fatalf("Map leaked a shared container: %v", again)
		}
	}
}

// rawMapDuration demonstrates the time.Duration / JSON-decoder issue:
// YAML decodes "30s" into the map as a string; json.Unmarshal cannot
// convert "30s" → int64 (time.Duration). A read-only transform sees the
// original string before the round-trip.
type rawMapDuration struct {
	TimeoutRaw string `json:"timeout"`
	Name       string `json:"name"`
}

func TestTransform_ObservesMergedMap(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte("timeout: 30s\nname: test\n"),
		},
	}

	var captured map[string]any
	cfg, err := fastconf.New[rawMapDuration](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransform(func(root map[string]any) error {
			// Copy the map so we can inspect it after the call.
			captured = make(map[string]any, len(root))
			for k, v := range root {
				captured[k] = v
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cfg.Close() }()

	if captured == nil {
		t.Fatal("transform was never called")
	}
	if v, _ := captured["timeout"].(string); v != "30s" {
		t.Fatalf("expected captured[timeout]=\"30s\", got %v", captured["timeout"])
	}
	if v, _ := captured["name"].(string); v != "test" {
		t.Fatalf("expected captured[name]=\"test\", got %v", captured["name"])
	}

	got := cfg.Get()
	if got.TimeoutRaw != "30s" {
		t.Fatalf("expected TimeoutRaw=\"30s\", got %q", got.TimeoutRaw)
	}
}

// rawMapProtocols demonstrates the json.RawMessage / YAML-decoder issue:
// yaml.Marshal + yaml.Unmarshal cannot decode a !!map into json.RawMessage.
// A read-only transform captures the sub-tree; a validator marshals it to
// JSON and injects the result.
type rawMapProtocols struct {
	Name      string          `json:"name"`
	Protocols json.RawMessage `json:"protocols"`
}

func TestTransform_CapturesProtocolsSubtree(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte("name: svc\nprotocols:\n  http:\n    port: 80\n  grpc:\n    port: 9090\n"),
		},
	}

	var rawProto atomic.Value // stores map[string]any

	cfg, err := fastconf.New[rawMapProtocols](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransform(func(root map[string]any) error {
			if p, ok := root["protocols"].(map[string]any); ok {
				rawProto.Store(p)
			}
			return nil
		}),
		fastconf.WithValidate(func(cfg *rawMapProtocols) error {
			if p, ok := rawProto.Load().(map[string]any); ok && p != nil {
				b, err := json.Marshal(p)
				if err != nil {
					return err
				}
				cfg.Protocols = b
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cfg.Close() }()

	got := cfg.Get()
	if got.Name != "svc" {
		t.Fatalf("expected Name=\"svc\", got %q", got.Name)
	}
	var protoMap map[string]any
	if err := json.Unmarshal(got.Protocols, &protoMap); err != nil {
		t.Fatalf("Protocols is not valid JSON: %v (raw=%s)", err, got.Protocols)
	}
	if _, ok := protoMap["http"]; !ok {
		t.Fatalf("expected \"http\" key in protocols, got %v", protoMap)
	}
	if _, ok := protoMap["grpc"]; !ok {
		t.Fatalf("expected \"grpc\" key in protocols, got %v", protoMap)
	}
}

// TestMapAnyTarget_Get demonstrates the no-struct escape hatch: when T is
// map[string]any, FastConf still works as a config loader, but loses the
// field-level compile-time check and zero-alloc snapshot.
func TestMapAnyTarget_Get(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte("server:\n  addr: \"127.0.0.1:8080\"\n  port: 8080\n"),
		},
	}
	m, err := fastconf.New[map[string]any](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	cfg := *m.Get()
	server, ok := cfg["server"].(map[string]any)
	if !ok {
		t.Fatalf("expected server map, got %T", cfg["server"])
	}
	if server["addr"] != "127.0.0.1:8080" {
		t.Errorf("addr = %v", server["addr"])
	}

	// Dotted-path lookup on the snapshot tree.
	if got, _ := confmap.GetDotted(m.Snapshot().Map(), "server.addr"); got != "127.0.0.1:8080" {
		t.Errorf("GetDotted(Map(), server.addr) = %v", got)
	}
}

// State.Explain must report the layer value as it appeared in the file, not
// the merged tree after the secret stage rewrote it in place. Origin.Value is
// documented as the per-layer contribution, and the pipeline deliberately
// orders the secret stage after transform so resolved plaintext is not
// exposed to earlier stages -- provenance, an operator-facing surface, must
// honour the same boundary.

type provSecretResolver struct{}

func (provSecretResolver) Recognize(v string) (fastconf.SecretRef, bool) {
	if rest, ok := strings.CutPrefix(v, "probe://"); ok {
		return fastconf.SecretRef{Scheme: "probe", Body: rest}, true
	}
	return fastconf.SecretRef{}, false
}

func (provSecretResolver) Resolve(_ context.Context, r fastconf.SecretRef) (string, error) {
	return "PLAINTEXT-" + r.Body, nil
}

type provSecretCfg struct {
	Items []struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	} `json:"items"`
}

func TestExplainDoesNotLeakResolvedSecretsThroughSliceLayers(t *testing.T) {
	fsys := fstest.MapFS{
		"conf.d/base/a.yaml": &fstest.MapFile{Data: []byte(
			"items:\n  - name: db\n    token: probe://dbpass\n")},
	}
	mgr, err := fastconf.New[provSecretCfg](context.Background(),
		fastconf.WithFS(fsys),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvenance(fastconf.ProvenanceFull),
		fastconf.WithSecretResolver(provSecretResolver{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// The decoded value still resolves, so the fix does not disable secrets.
	if got := mgr.Get().Items[0].Token; got != "PLAINTEXT-dbpass" {
		t.Fatalf("decoded token = %q, want the resolved plaintext", got)
	}

	origins := mgr.Snapshot().Explain("items")
	if len(origins) == 0 {
		t.Fatal("Explain(items) recorded no origin")
	}
	for _, o := range origins {
		rendered := renderProvValue(o.Value)
		if strings.Contains(rendered, "PLAINTEXT-") {
			t.Errorf("Explain(%q) leaked resolved plaintext: %s", o.Path, rendered)
		}
		if !strings.Contains(rendered, "probe://dbpass") {
			t.Errorf("Explain(%q) = %s, want the pre-resolution reference", o.Path, rendered)
		}
	}
}

func renderProvValue(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case string:
			b.WriteString(x)
			b.WriteByte(' ')
		}
	}
	walk(v)
	return b.String()
}
