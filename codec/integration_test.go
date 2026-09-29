package codec_test

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
)

type typedCfg struct {
	Timeout time.Duration `json:"timeout"`
	Server  struct {
		ReadTimeout time.Duration `json:"readTimeout"`
	} `json:"server"`
}

func TestTypedHook_DurationFromYAML(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(`
timeout: 30s
server:
  readTimeout: 1500ms
`)},
	}
	mgr, err := fastconf.New[typedCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	cfg := mgr.Get()
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if cfg.Server.ReadTimeout != 1500*time.Millisecond {
		t.Errorf("ReadTimeout = %v, want 1500ms", cfg.Server.ReadTimeout)
	}
}

// Custom hook: parse a string into an int that the JSON decoder will
// natively accept for the rune-typed Mood field below.
type moodHook struct{}

type Mood int

func (moodHook) Match(t reflect.Type) bool {
	return t == reflect.TypeFor[Mood]()
}

func (moodHook) Convert(raw any) (any, error) {
	switch v := raw.(type) {
	case string:
		switch v {
		case "happy":
			return 1, nil
		case "sad":
			return -1, nil
		default:
			return 0, nil
		}
	default:
		return raw, nil
	}
}

type withMoodCfg struct {
	Mood Mood `json:"mood"`
}

func TestWithTypedHook_Custom(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("mood: happy\n")},
	}
	mgr, err := fastconf.New[withMoodCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTypedHook(moodHook{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if mgr.Get().Mood != 1 {
		t.Errorf("Mood = %v, want 1", mgr.Get().Mood)
	}
}

func TestWithoutDefaultTypedHooks_BreaksDuration(t *testing.T) {
	fs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("timeout: 30s\n")},
	}
	// Without the Duration hook the string "30s" cannot be unmarshalled
	// into time.Duration — verify the decode failure surfaces.
	_, err := fastconf.New[typedCfg](context.Background(),
		fastconf.WithFS(fs),
		fastconf.WithDir("conf.d"),
		fastconf.WithoutDefaultTypedHooks(),
	)
	if err == nil {
		t.Error("expected decode error when defaults disabled")
	}
}

// fakeCodec parses a tiny "k=v" line format to prove that a third-party
// codec slots into the pipeline through the public registry alone.
type fakeCodec struct{}

func (fakeCodec) Decode(data []byte) (map[string]any, error) {
	out := map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out, nil
}

type kvCfg struct {
	Server string `yaml:"server"`
}

func TestRegister_PluggableThirdPartyFormat(t *testing.T) {
	codec.Register("kv", fakeCodec{})
	codec.RegisterExt("kv", "kv")
	defer func() {
		// Re-register a no-op to leave the global registry in a known state
		// for subsequent tests; lookup tests below verify presence anyway.
	}()

	if _, ok := codec.Lookup("kv"); !ok {
		t.Fatalf("Register did not surface in Lookup")
	}

	mfs := fstest.MapFS{
		"conf.d/base/00-app.kv": &fstest.MapFile{Data: []byte("server=:7777\n")},
	}
	cfg, err := fastconf.New[kvCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = cfg.Close() }()

	if got := cfg.Get().Server; got != ":7777" {
		t.Fatalf("got %q, want :7777", got)
	}
}

func TestCodec_UnknownExtensionStillRejected(t *testing.T) {
	mfs := fstest.MapFS{"conf.d/base/00.hcl": &fstest.MapFile{Data: []byte("a = 1")}}
	_, err := fastconf.New[struct{ A int }](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"), fastconf.WithStrictMerge(true),
	)
	if err == nil {
		t.Fatal("expected unknown-extension error for .hcl in strict mode")
	}
}

func TestRegister_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil codec registration")
		}
	}()
	codec.Register("nilcodec", nil)
}

// TestTOMLCodec_EndToEnd verifies that a `.toml` configuration layer
// is discovered, decoded, deep-merged, and decoded into *T through
// the full reload pipeline. The built-in TOML codec was introduced in
// v0.8; this test pins the contract from discovery through Get.
func TestTOMLCodec_EndToEnd(t *testing.T) {
	type Server struct {
		Addr string `json:"addr" toml:"addr"`
		Port int    `json:"port" toml:"port"`
	}
	type Cfg struct {
		Name   string `json:"name" toml:"name"`
		Debug  bool   `json:"debug" toml:"debug"`
		Server Server `json:"server" toml:"server"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00-app.toml": &fstest.MapFile{Data: []byte(`
name = "edge"
debug = true

[server]
addr = "0.0.0.0"
port = 8080
`)},
	}

	mgr, err := fastconf.New[Cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	got := mgr.Get()
	if got == nil {
		t.Fatal("Get returned nil")
		return
	}
	if got.Name != "edge" {
		t.Errorf("Name = %q, want edge", got.Name)
	}
	if !got.Debug {
		t.Errorf("Debug = false, want true")
	}
	if got.Server.Addr != "0.0.0.0" {
		t.Errorf("Server.Addr = %q, want 0.0.0.0", got.Server.Addr)
	}
	if got.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", got.Server.Port)
	}

	// Source provenance must carry the toml codec name.
	snap := mgr.Snapshot()
	if len(snap.Sources()) == 0 {
		t.Fatal("snapshot has no Sources")
	}
	if snap.Sources()[0].Codec != "toml" {
		t.Errorf("Source[0].Codec = %q, want toml", snap.Sources()[0].Codec)
	}
}

// TestTOMLCodec_OverlayPatchYAML pairs a TOML base with a YAML overlay
// to exercise the cross-codec merge path. Discovery picks each codec
// per-file via the registered extension table.
func TestTOMLCodec_OverlayPatchYAML(t *testing.T) {
	type Cfg struct {
		Name  string `json:"name" yaml:"name" toml:"name"`
		Stage string `json:"stage" yaml:"stage" toml:"stage"`
	}

	mfs := fstest.MapFS{
		"conf.d/base/00-app.toml":          &fstest.MapFile{Data: []byte(`name = "base"` + "\n" + `stage = "dev"`)},
		"conf.d/overlays/prod/00-app.yaml": &fstest.MapFile{Data: []byte("stage: prod\n")},
	}

	mgr, err := fastconf.New[Cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	got := mgr.Get()
	if got.Name != "base" {
		t.Errorf("Name = %q, want base", got.Name)
	}
	if got.Stage != "prod" {
		t.Errorf("Stage = %q, want prod (overlay wins)", got.Stage)
	}
}

// TestNew_WarnsOnYAMLOnlyTags verifies that when *T has yaml struct tags but
// no json tags, New emits a warn-level log so the operator notices the default
// the JSON decoder ignoring those tags. Skipping the warning when WithDecoder(YAML) is
// selected is exercised in the second subtest.
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
		defer func() { _ = mgr.Close() }()
		out := buf.String()
		if !strings.Contains(out, "yaml tags") || !strings.Contains(out, "WithDecoder(YAML)") {
			t.Errorf("expected yaml-only-tag warning, got:\n%s", out)
		}
		if !strings.Contains(out, "yamlOnly") {
			t.Errorf("expected struct type name in warn payload, got:\n%s", out)
		}
	})

	t.Run("YAML decoder suppresses warn", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		mgr, err := fastconf.New[yamlOnly](context.Background(),
			fastconf.WithFS(mfs),
			fastconf.WithDir("conf.d"),
			fastconf.WithLogger(logger),
			fastconf.WithDecoder(fastconf.YAML),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = mgr.Close() }()
		if strings.Contains(buf.String(), "yaml tags") {
			t.Errorf("WithDecoder(YAML) should suppress warn, got:\n%s", buf.String())
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
		defer func() { _ = mgr.Close() }()
		if strings.Contains(buf.String(), "yaml tags") {
			t.Errorf("json-tagged struct should not warn, got:\n%s", buf.String())
		}
	})

	t.Run("fc metadata does not suppress warning", func(t *testing.T) {
		type yamlWithFC struct {
			DBPool int `yaml:"db_pool" fc:"default=1"`
		}
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		mgr, err := fastconf.New[yamlWithFC](context.Background(),
			fastconf.WithFS(mfs),
			fastconf.WithDir("conf.d"),
			fastconf.WithLogger(logger),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = mgr.Close() }()
		if !strings.Contains(buf.String(), "yaml tags") {
			t.Errorf("fc metadata should not suppress yaml-only warning, got:\n%s", buf.String())
		}
	})
}
