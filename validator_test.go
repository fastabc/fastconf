package fastconf_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

type validatorCfg struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
}

func validatorFS(addr string) fstest.MapFS {
	return fstest.MapFS{
		"conf.d/base/00-app.yaml": &fstest.MapFile{
			Data: []byte("server:\n  addr: \"" + addr + "\"\n"),
		},
	}
}

func TestWithValidator_BlocksBadConfig(t *testing.T) {
	_, err := fastconf.New[validatorCfg](context.Background(),
		fastconf.WithFS(validatorFS("")), fastconf.WithDir("conf.d"),
		fastconf.WithValidator(func(c *validatorCfg) error {
			if c.Server.Addr == "" {
				return errors.New("server.addr required")
			}
			return nil
		}),
	)
	if err == nil {
		t.Fatalf("expected validator error, got nil")
	}
	if !errors.Is(err, fastconf.ErrValidator) {
		t.Fatalf("want ErrValidator, got %v", err)
	}
}

func TestWithValidator_AllowsGoodConfig(t *testing.T) {
	cfg, err := fastconf.New[validatorCfg](context.Background(),
		fastconf.WithFS(validatorFS(":8080")), fastconf.WithDir("conf.d"),
		fastconf.WithValidator(func(c *validatorCfg) error {
			if c.Server.Addr == "" {
				return errors.New("required")
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()
	if got := cfg.Get().Server.Addr; got != ":8080" {
		t.Fatalf("got %q", got)
	}
}

func TestWithValidator_NilSafe(t *testing.T) {
	_, err := fastconf.New[validatorCfg](context.Background(),
		fastconf.WithFS(validatorFS(":1")), fastconf.WithDir("conf.d"),
		fastconf.WithValidator[validatorCfg](nil),
	)
	if err != nil {
		t.Fatalf("nil validator should be a no-op, got %v", err)
	}
}

