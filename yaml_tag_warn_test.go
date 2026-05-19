package fastconf_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

// TestNew_WarnsOnYAMLOnlyTags pins SPEC-A5: when *T has yaml struct
// tags but no json/fastconf tags, New emits a warn-level log so the
// operator notices the default BridgeJSON ignoring those tags. Skipping
// the warning when BridgeYAML is selected is exercised in the second
// subtest.
func TestNew_WarnsOnYAMLOnlyTags(t *testing.T) {
	type yamlOnly struct {
		DBPool int    `yaml:"db_pool"`
		Addr   string `yaml:"addr"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("db_pool: 1\naddr: x\n")},
	}

	t.Run("default bridge warns", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		mgr, err := fastconf.New[yamlOnly](context.Background(),
			fastconf.WithFS(mfs),
			fastconf.WithDir("conf.d"),
			fastconf.WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer mgr.Close()
		out := buf.String()
		if !strings.Contains(out, "yaml tags") || !strings.Contains(out, "BridgeYAML") {
			t.Errorf("expected yaml-only-tag warning, got:\n%s", out)
		}
		if !strings.Contains(out, "yamlOnly") {
			t.Errorf("expected struct type name in warn payload, got:\n%s", out)
		}
	})

	t.Run("BridgeYAML suppresses warn", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		mgr, err := fastconf.New[yamlOnly](context.Background(),
			fastconf.WithFS(mfs),
			fastconf.WithDir("conf.d"),
			fastconf.WithLogger(logger),
			fastconf.WithCodecBridge(fastconf.BridgeYAML),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer mgr.Close()
		if strings.Contains(buf.String(), "yaml tags") {
			t.Errorf("BridgeYAML should suppress warn, got:\n%s", buf.String())
		}
	})

	t.Run("json-tagged struct is silent", func(t *testing.T) {
		type jsonOK struct {
			DBPool int `json:"db_pool"`
		}
		jsonFS := fstest.MapFS{
			"conf.d/base/00.json": &fstest.MapFile{Data: []byte(`{"db_pool": 1}`)},
		}
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		mgr, err := fastconf.New[jsonOK](context.Background(),
			fastconf.WithFS(jsonFS),
			fastconf.WithDir("conf.d"),
			fastconf.WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer mgr.Close()
		if strings.Contains(buf.String(), "yaml tags") {
			t.Errorf("json-tagged struct should not warn, got:\n%s", buf.String())
		}
	})
}
