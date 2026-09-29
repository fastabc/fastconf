package transform_test

import (
	"context"
	"errors"
	"strings"
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
