package fastconf_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/transform"
)

// TestTransformer_AliasIdentity pins the Phase 82 contract: the
// fastconf.Transformer name is a type alias for transform.Transformer,
// so their reflect.Type values are identical. Re-introducing a
// separate interface declaration would silently break callers that
// share Transformer values across the package boundary.
func TestTransformer_AliasIdentity(t *testing.T) {
	a := reflect.TypeFor[fastconf.Transformer]()
	b := reflect.TypeFor[transform.Transformer]()
	if a != b {
		t.Fatalf("fastconf.Transformer != transform.Transformer: %v vs %v", a, b)
	}
	// Concrete value round-trip via the alias surface.
	var tr fastconf.Transformer = transform.TransformerFunc{
		NameStr: "noop",
		Fn:      func(map[string]any) error { return nil },
	}
	if tr.Name() != "noop" {
		t.Fatalf("Name()=%q want noop", tr.Name())
	}
}

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

func TestWithTransformers_RunsInOrder(t *testing.T) {
	mfs := transformFS(`
server:
  addr: 0.0.0.0
db:
  dsn: ${DB_DSN:-postgres://x}
`)
	cfg, err := fastconf.New[transformCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransformers(
			transform.EnvSubstWith(func(string) string { return "" }),
			transform.Aliases(map[string]string{"db.dsn": "database.dsn"}),
			transform.Defaults(map[string]any{
				"server": map[string]any{"port": 8080},
			}),
		),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()
	v := cfg.Get()
	if v.Server.Addr != "0.0.0.0" {
		t.Errorf("addr: %v", v.Server.Addr)
	}
	if v.Server.Port != 8080 {
		t.Errorf("default port not applied: %v", v.Server.Port)
	}
	if v.Database.DSN != "postgres://x" {
		t.Errorf("alias+envsubst chain wrong: %v", v.Database.DSN)
	}
}

func TestWithTransformers_FailureBlocksCommit(t *testing.T) {
	mfs := transformFS(`server: {addr: "0.0.0.0", port: 8080}`)
	boom := errors.New("nope")
	_, err := fastconf.New[transformCfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransformers(transform.TransformerFunc{
			NameStr: "boom",
			Fn:      func(map[string]any) error { return boom },
		}),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, fastconf.ErrTransform) {
		t.Fatalf("expected ErrTransform, got %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected transformer name in error, got %v", err)
	}
}
