package fastconf_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

// rawMapDuration demonstrates the time.Duration / BridgeJSON issue:
// YAML decodes "30s" into the map as a string; json.Unmarshal cannot
// convert "30s" → int64 (time.Duration).  WithRawMapAccess lets the
// adapter read the original string before the round-trip.
type rawMapDuration struct {
	TimeoutRaw string `json:"timeout"`
	Name       string `json:"name"`
}

func TestWithRawMapAccess_CapturesMap(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte("timeout: 30s\nname: test\n"),
		},
	}

	var captured map[string]any
	cfg, err := fastconf.New[rawMapDuration](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithRawMapAccess(func(root map[string]any) {
			// Copy the map so we can inspect it after the call.
			captured = make(map[string]any, len(root))
			for k, v := range root {
				captured[k] = v
			}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()

	if captured == nil {
		t.Fatal("rawMapHook was never called")
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

// rawMapProtocols demonstrates the json.RawMessage / BridgeYAML issue:
// yaml.Marshal + yaml.Unmarshal cannot decode a !!map into json.RawMessage.
// WithRawMapAccess lets the adapter capture the sub-tree and marshal it to
// JSON independently, then inject the result via a validator.
type rawMapProtocols struct {
	Name      string          `json:"name"`
	Protocols json.RawMessage `json:"protocols"`
}

func TestWithRawMapAccess_ProtocolsSubtree(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte("name: svc\nprotocols:\n  http:\n    port: 80\n  grpc:\n    port: 9090\n"),
		},
	}

	var rawProto atomic.Value // stores map[string]any

	cfg, err := fastconf.New[rawMapProtocols](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithRawMapAccess(func(root map[string]any) {
			if p, ok := root["protocols"].(map[string]any); ok {
				rawProto.Store(p)
			}
		}),
		fastconf.WithValidator(func(cfg *rawMapProtocols) error {
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
	defer cfg.Close()

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

// TestWithRawMapAccess_NilNoop verifies that passing nil is a no-op and
// does not panic during reload.
func TestWithRawMapAccess_NilNoop(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{
			Data: []byte("name: noop\n"),
		},
	}
	cfg, err := fastconf.New[struct {
		Name string `json:"name"`
	}](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithRawMapAccess(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	if cfg.Get().Name != "noop" {
		t.Fatalf("unexpected value: %v", cfg.Get())
	}
}

// TestMapAnyTarget_Get demonstrates SPEC-126's "fallback for the
// no-struct user": when T is map[string]any, FastConf still works as
// a config loader, but loses the field-level compile-time check and
// zero-alloc snapshot. Documented as the escape hatch in README §1.1.
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
	defer m.Close()

	cfg := *m.Get()
	server, ok := cfg["server"].(map[string]any)
	if !ok {
		t.Fatalf("expected server map, got %T", cfg["server"])
	}
	if server["addr"] != "127.0.0.1:8080" {
		t.Errorf("addr = %v", server["addr"])
	}

	// Introspection path: flat dotted-key view via AllSettings (the
	// implementation flattens nested structures, so addressable as
	// "server.addr" rather than nested map access).
	state := m.Snapshot()
	if got := state.Introspect().Settings()["server.addr"]; got != "127.0.0.1:8080" {
		t.Errorf("Introspect().Settings()[server.addr] = %v", got)
	}
}
