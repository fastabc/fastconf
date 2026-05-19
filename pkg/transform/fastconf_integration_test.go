package transform_test

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

// TestTransformer_StructuralIdentity pins the SPEC-A3 contract: root
// fastconf.Transformer and pkg/transform.Transformer are SEPARATE named
// interfaces with the identical method set, so a concrete value
// implementing one automatically satisfies the other (Go structural
// typing) — but reflect.TypeFor returns distinct identities. Re-introducing
// a Go type alias here would re-couple the root package's public API to
// pkg/transform's internal evolution.
func TestTransformer_StructuralIdentity(t *testing.T) {
	a := reflect.TypeFor[fastconf.Transformer]()
	b := reflect.TypeFor[transform.Transformer]()
	if a == b {
		t.Fatalf("expected distinct reflect.Type for the two named interfaces; got %v", a)
	}
	if a.Kind() != reflect.Interface || b.Kind() != reflect.Interface {
		t.Fatalf("both must be interfaces; got %v / %v", a.Kind(), b.Kind())
	}
	if a.NumMethod() != b.NumMethod() {
		t.Fatalf("method-set size mismatch: %d vs %d", a.NumMethod(), b.NumMethod())
	}
	// A concrete value of transform.TransformerFunc satisfies BOTH
	// interfaces — structural typing covers the seam.
	var tr fastconf.Transformer = transform.TransformerFunc{
		NameStr: "noop",
		Fn:      func(map[string]any) error { return nil },
	}
	if tr.Name() != "noop" {
		t.Fatalf("Name()=%q want noop", tr.Name())
	}
	var tr2 transform.Transformer = tr
	if tr2.Name() != "noop" {
		t.Fatalf("cross-interface assignment lost identity: %q", tr2.Name())
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
