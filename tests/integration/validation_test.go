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

type removedDefaultsCfg struct {
	Host   string `json:"host" fc:"default=localhost"`
	Nested struct {
		Quota int `json:"quota" fc:"min=1,default=42"`
	} `json:"nested"`
	Items []struct {
		Port int `json:"port" fc:"default=8080"`
	} `json:"items"`
}

// fc:"default=…" was removed in v1.7.0. Any remaining declaration, including
// one inside a collection element, must fail construction instead of being
// ignored silently.
func TestStructDefaults_RemovedFCDefaultFailsConstruction(t *testing.T) {
	type top struct {
		Host string `json:"host" fc:"default=localhost"`
	}
	type element struct {
		Items []struct {
			Port int `json:"port" fc:"default=8080"`
		} `json:"items"`
	}
	for name, err := range map[string]error{
		"top":     newRemovedDefault[top](),
		"element": newRemovedDefault[element](),
		"mixed":   newRemovedDefault[removedDefaultsCfg](),
	} {
		if !errors.Is(err, fastconf.ErrFastConf) || !strings.Contains(err.Error(), `fc:"default=…" was removed`) {
			t.Errorf("%s: err = %v; want removed fc default", name, err)
		}
	}
	_, err := fastconf.Load[top](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("over", "yaml", []byte("host: x\n"))))
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("Load error should name the field: %v", err)
	}
}

func newRemovedDefault[T any]() error {
	m, err := fastconf.New[T](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("over", "yaml", []byte("{}\n"))))
	if m != nil {
		_ = m.Close()
	}
	return err
}

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
