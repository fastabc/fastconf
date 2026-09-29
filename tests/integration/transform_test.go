package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	transform "github.com/fastabc/fastconf/transform"
)

type transformCfg struct {
	Server struct {
		Addr string `yaml:"addr"`
		Port int    `yaml:"port"`
	} `yaml:"server"`
	Database struct {
		DSN string `yaml:"dsn"`
	} `yaml:"database"`
}

func transformFS(yaml string) fstest.MapFS {
	return fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{Data: []byte(yaml)},
	}
}

func TestWithTransform_RunsInOrder(t *testing.T) {
	mfs := transformFS(`
server:
  addr: 0.0.0.0
db:
  dsn: ${DB_DSN:-postgres://x}
`)
	cfg, err := fastconf.New[transformCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransform(
			transform.EnvSubstWith(func(string) string { return "" }),
			transform.Aliases(map[string]string{"db.dsn": "database.dsn"}),
			transform.DeletePaths("server.addr"),
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	v := cfg.Get()
	if v.Server.Addr != "" {
		t.Errorf("addr not deleted: %v", v.Server.Addr)
	}
	if v.Database.DSN != "postgres://x" {
		t.Errorf("alias+envsubst chain wrong: %v", v.Database.DSN)
	}
}

func TestWithTransform_FailureBlocksCommit(t *testing.T) {
	mfs := transformFS(`server: {addr: "0.0.0.0", port: 8080}`)
	boom := errors.New("nope")
	_, err := fastconf.New[transformCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransform(func(map[string]any) error { return boom }),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, fastconf.ErrTransform) {
		t.Fatalf("expected ErrTransform, got %v", err)
	}
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "transform[0]") {
		t.Fatalf("expected the cause and the transform index in error, got %v", err)
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
