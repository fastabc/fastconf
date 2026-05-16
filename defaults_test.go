package fastconf_test

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"

	"github.com/fastabc/fastconf/pkg/provider"
)

// emptyConfFS returns an FS with one placeholder file under conf.d/base
// so discovery yields no real layers. Tests that exercise providers,
// bytes sources, or struct defaults use this to neutralise the
// file-discovery layer. Mirrors the internal emptyFS() helper used by
// in-package tests (history_test.go); duplicated here for the
// fastconf_test package since Go does not let one test package import
// another test file.
func emptyConfFS() fstest.MapFS {
	return fstest.MapFS{
		"conf.d/base/.keep": &fstest.MapFile{Data: []byte{}},
	}
}

// TestDefaultsAreStable guards against accidental changes to user-visible
// defaults that would silently break running deployments.
func TestDefaultsAreStable(t *testing.T) {
	if fastconf.DefaultDir != "conf.d" {
		t.Errorf("DefaultDir = %q, want %q", fastconf.DefaultDir, "conf.d")
	}
	if fastconf.DefaultProfileEnv != "APP_PROFILE" {
		t.Errorf("DefaultProfileEnv = %q, want %q", fastconf.DefaultProfileEnv, "APP_PROFILE")
	}
	if fastconf.DefaultDebounceInterval != 500*time.Millisecond {
		t.Errorf("DefaultDebounceInterval = %v, want 500ms", fastconf.DefaultDebounceInterval)
	}
	if fastconf.DefaultSidecarHistoryCap != 16 {
		t.Errorf("DefaultSidecarHistoryCap = %d, want 16", fastconf.DefaultSidecarHistoryCap)
	}
}

// ── Folded from defaults_internal_test.go (Phase 84 SPEC-84) ──

type defaultsCfg struct {
	Host    string `json:"host" fastconf:"default=localhost"`
	Port    int    `json:"port" fastconf:"default=8080"`
	Enabled bool   `json:"enabled" fastconf:"default=true"`
	Nested  struct {
		Quota int `json:"quota" fastconf:"default=42"`
	} `json:"nested"`
	NoDefault string `json:"no_default"`
}

func TestStructDefaults_FillsZeroFields(t *testing.T) {
	mgr, err := fastconf.New[defaultsCfg](context.Background(),
		fastconf.WithFS(emptyConfFS()),
		fastconf.WithProvider(provider.NewBytes("over", "yaml", []byte("port: 9090\n"))),
		fastconf.WithStructDefaults[defaultsCfg](),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	got := mgr.Get()
	if got.Host != "localhost" {
		t.Fatalf("Host=%q", got.Host)
	}
	if got.Port != 9090 {
		t.Fatalf("user override lost; Port=%d", got.Port)
	}
	if !got.Enabled {
		t.Fatalf("Enabled default not applied")
	}
	if got.Nested.Quota != 42 {
		t.Fatalf("nested default not applied: %d", got.Nested.Quota)
	}
	if got.NoDefault != "" {
		t.Fatalf("untagged field altered: %q", got.NoDefault)
	}
}

type defaultsSliceCfg struct {
	Items []struct {
		Port int `json:"port" fastconf:"default=8080"`
	} `json:"items"`
}

func TestStructDefaults_DoesNotPlanSliceElementDefaults(t *testing.T) {
	mgr, err := fastconf.New[defaultsSliceCfg](context.Background(),
		fastconf.WithFS(emptyConfFS()),
		fastconf.WithProvider(provider.NewBytes("over", "yaml", []byte("items: []\n"))),
		fastconf.WithStructDefaults[defaultsSliceCfg](),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if got := mgr.Get(); len(got.Items) != 0 {
		t.Fatalf("Items len=%d", len(got.Items))
	}
}

type defaulterFuncCfg struct {
	Seed    string `json:"seed" fastconf:"default=seed"`
	Derived string `json:"derived"`
}

func TestDefaulterFunc_RunsAfterStructDefaultsBeforeValidator(t *testing.T) {
	mgr, err := fastconf.New[defaulterFuncCfg](context.Background(),
		fastconf.WithFS(emptyConfFS()),
		fastconf.WithProvider(provider.NewBytes("over", "yaml", []byte("{}\n"))),
		fastconf.WithStructDefaults[defaulterFuncCfg](),
		fastconf.WithDefaulterFunc(func(c *defaulterFuncCfg) {
			c.Derived = c.Seed + "-derived"
		}),
		fastconf.WithValidator(func(c *defaulterFuncCfg) error {
			if c.Seed != "seed" || c.Derived != "seed-derived" {
				return fmt.Errorf("validator saw seed=%q derived=%q", c.Seed, c.Derived)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if got := mgr.Get().Derived; got != "seed-derived" {
		t.Fatalf("Derived=%q want seed-derived", got)
	}
}

func TestDefaulterFunc_NilIsNoop(t *testing.T) {
	mgr, err := fastconf.New[defaulterFuncCfg](context.Background(),
		fastconf.WithFS(emptyConfFS()),
		fastconf.WithProvider(provider.NewBytes("over", "yaml", []byte("seed: explicit\n"))),
		fastconf.WithDefaulterFunc[defaulterFuncCfg](nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if got := mgr.Get().Seed; got != "explicit" {
		t.Fatalf("Seed=%q want explicit", got)
	}
}
