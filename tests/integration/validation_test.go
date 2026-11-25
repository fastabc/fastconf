package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
)

type defaulterFuncCfg struct {
	Seed    string `json:"seed" default:"seed"`
	Derived string `json:"derived"`
}

// Defaults implements fastconf.Defaulter.
func (c *defaulterFuncCfg) Defaults() { c.Derived = c.Seed + "-derived" }

func TestDefaulter_RunsAfterTagDefaultsBeforeValidator(t *testing.T) {
	mgr, err := fastconf.New[defaulterFuncCfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("over", "yaml", []byte("{}\n"))),
		fastconf.WithValidate(func(c *defaulterFuncCfg) error {
			if c.Seed != "seed" || c.Derived != "seed-derived" {
				return fmt.Errorf("validator saw seed=%q derived=%q", c.Seed, c.Derived)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Get().Derived; got != "seed-derived" {
		t.Fatalf("Derived=%q want seed-derived", got)
	}
}

type fieldMetaConfig struct {
	Server struct {
		Addr string `json:"addr" fc:"required"`
		Port int    `json:"port" fc:"min=1,max=65535"`
	} `json:"server"`
	LogLevel string `json:"log_level" fc:"oneof=info|warn|error,desc=日志级别"`
}

func TestFieldMeta_Validation(t *testing.T) {
	for _, tc := range []struct{ name, body, path string }{
		{"valid", "server: {addr: ':80', port: 80}\nlog_level: info\n", ""},
		{"required", "server: {port: 80}\nlog_level: info\n", "server.addr"},
		{"minimum", "server: {addr: ':80', port: 0}\nlog_level: info\n", "server.port"},
		{"maximum", "server: {addr: ':80', port: 65536}\nlog_level: info\n", "server.port"},
		{"oneof", "server: {addr: ':80', port: 80}\nlog_level: trace\n", "log_level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := fastconf.Load[fieldMetaConfig](context.Background(), fastconf.WithFS(fstest.MapFS{"conf.d/base/00.yaml": {Data: []byte(tc.body)}}))
			if tc.path != "" {
				if snap != nil || !errors.Is(err, fastconf.ErrInvalid) || !strings.Contains(err.Error(), tc.path) {
					t.Fatalf("Load=%v, %v; want ErrInvalid naming %s", snap, err, tc.path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := snap.Value(); got.Server.Addr != ":80" || got.Server.Port != 80 || got.LogLevel != "info" {
				t.Fatalf("config=%+v", got)
			}
		})
	}
}
