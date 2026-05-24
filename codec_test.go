package fastconf_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

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

func TestRegisterCodec_PluggableThirdPartyFormat(t *testing.T) {
	fastconf.RegisterCodec("kv", fakeCodec{})
	fastconf.RegisterCodecExt("kv", "kv")
	defer func() {
		// Re-register a no-op to leave the global registry in a known state
		// for subsequent tests; lookup tests below verify presence anyway.
	}()

	if _, ok := fastconf.LookupCodec("kv"); !ok {
		t.Fatalf("RegisterCodec did not surface in LookupCodec")
	}

	mfs := fstest.MapFS{
		"conf.d/base/00-app.kv": &fstest.MapFile{Data: []byte("server=:7777\n")},
	}
	cfg, err := fastconf.New[kvCfg](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()

	if got := cfg.Get().Server; got != ":7777" {
		t.Fatalf("got %q, want :7777", got)
	}
}

// TestCodec_TOMLBuiltIn verifies that .toml files are decoded by the
// built-in BurntSushi/toml backend without any explicit registration.
// Replaces the pre-Phase-90 TOMLWithoutRegistrationIsUnknown contract:
// TOML is now first-class alongside YAML and JSON. Third-party codecs
// (HCL, JSON5, ...) still require explicit RegisterCodec.
func TestCodec_TOMLBuiltIn(t *testing.T) {
	mfs := fstest.MapFS{"conf.d/base/00.toml": &fstest.MapFile{Data: []byte("a = 1")}}
	cfg, err := fastconf.New[struct {
		A int `json:"a" toml:"a"`
	}](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"), fastconf.WithStrict(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cfg.Close()
	if got := cfg.Get().A; got != 1 {
		t.Fatalf("A = %d, want 1", got)
	}
}

// TestCodec_UnknownExtensionStillRejected pins the strict-mode contract
// for genuinely unrecognised extensions: even after TOML was added,
// .hcl / .ini / .json5 must still error unless RegisterCodecExt wires
// them in.
func TestCodec_UnknownExtensionStillRejected(t *testing.T) {
	mfs := fstest.MapFS{"conf.d/base/00.hcl": &fstest.MapFile{Data: []byte("a = 1")}}
	_, err := fastconf.New[struct{ A int }](context.Background(),
		fastconf.WithFS(mfs), fastconf.WithDir("conf.d"), fastconf.WithStrict(true),
	)
	if err == nil {
		t.Fatal("expected unknown-extension error for .hcl in strict mode")
	}
}

func TestRegisterCodec_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil codec registration")
		}
	}()
	fastconf.RegisterCodec("nilcodec", nil)
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
	defer mgr.Close()

	got := mgr.Get()
	if got == nil {
		t.Fatal("Get returned nil")
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
		fastconf.WithProfile(fastconf.ProfileOptions{Single: "prod"}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	got := mgr.Get()
	if got.Name != "base" {
		t.Errorf("Name = %q, want base", got.Name)
	}
	if got.Stage != "prod" {
		t.Errorf("Stage = %q, want prod (overlay wins)", got.Stage)
	}
}
