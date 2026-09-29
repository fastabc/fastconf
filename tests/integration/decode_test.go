package integration_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
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

func TestYAMLBridge_DetectsJSONHiddenFieldChange(t *testing.T) {
	type cfg struct {
		Name     string `yaml:"name" json:"name"`
		Password string `yaml:"password" json:"-"`
	}
	fs := fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("name: app\npassword: one\n")}}
	observer := &recObserver{}
	m, err := fastconf.New[cfg](context.Background(), fastconf.WithFS(fs),
		fastconf.WithDecodeFormat(fastconf.YAML), fastconf.WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Snapshot()
	changes := make(chan [2]string, 2)
	defer fastconf.Subscribe(m, func(c *cfg) *string { return &c.Password }, func(old, next *string) {
		changes <- [2]string{*old, *next}
	})()
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("name: app\npassword: two\n")}
	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Proposed.Value().Password != "two" || plan.Proposed.Hash() == before.Hash() {
		t.Error("Plan must hash the updated YAML field")
	}
	if m.Snapshot() != before {
		t.Fatal("Plan published its preview")
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := m.Snapshot()
	if m.Get().Password != "two" || after.Generation() != before.Generation()+1 || after.Hash() == before.Hash() {
		t.Fatalf("reload dropped hidden field change: value=%q generation=%d", m.Get().Password, after.Generation())
	}
	if after.Hash() != plan.Proposed.Hash() {
		t.Fatal("Plan and Reload disagree on hash")
	}
	select {
	case got := <-changes:
		if got != [2]string{"one", "two"} {
			t.Fatalf("subscription = %v", got)
		}
	default:
		t.Fatal("missing subscription notification")
	}
	// Change the source bytes to force the pipeline while preserving the typed value.
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("# same config\nname: app\npassword: two\n")}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != after {
		t.Fatal("equal YAML value published a new snapshot")
	}
	commits := 0
	for _, event := range observer.snapshot() {
		if _, ok := event.(fastconf.Committed); ok {
			commits++
		}
	}
	if commits != 2 { // initial load and password rotation
		t.Fatalf("Committed events = %d, want 2", commits)
	}
	if len(before.Diff(after)) != 0 {
		t.Fatal("JSON-hidden fields should remain absent from diagnostic diffs")
	}
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

// TOML documents pass through discovery, merge, and the typed bridge.
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
// the JSON decoder ignoring those tags. Skipping the warning when WithDecodeFormat(YAML) is
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
		if !strings.Contains(out, "yaml tags") || !strings.Contains(out, "WithDecodeFormat(YAML)") {
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
			fastconf.WithDecodeFormat(fastconf.YAML),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() { _ = mgr.Close() }()
		if strings.Contains(buf.String(), "yaml tags") {
			t.Errorf("WithDecodeFormat(YAML) should suppress warn, got:\n%s", buf.String())
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

func loadYAML[T any](t *testing.T, body string, opts ...fastconf.Option) *T {
	t.Helper()
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte(body)}}
	opts = append([]fastconf.Option{fastconf.WithFS(fs), fastconf.WithDir("conf.d")}, opts...)
	st, err := fastconf.Load[T](context.Background(), opts...)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return st.Value()
}

type selfNode struct {
	Name    string        `json:"name" yaml:"name"`
	Timeout time.Duration `json:"timeout" yaml:"timeout"`
	Next    *selfNode     `json:"next" yaml:"next"`
}

type mutualA struct {
	B *mutualB `json:"b"`
}

type mutualB struct {
	Wait time.Duration `json:"wait"`
	A    *mutualA      `json:"a"`
}

func TestTypedHooks_SelfReferentialType(t *testing.T) {
	if got := loadYAML[selfNode](t, "{}\n"); got.Next != nil {
		t.Fatalf("empty input: %+v", got)
	}
	got := loadYAML[selfNode](t, "timeout: 1s\nnext:\n  timeout: 2s\n  next:\n    timeout: 3s\n")
	if got.Timeout != time.Second || got.Next.Timeout != 2*time.Second || got.Next.Next.Timeout != 3*time.Second {
		t.Fatalf("recursive durations not converted: %+v", got)
	}
	yamlGot := loadYAML[selfNode](t, "next:\n  next:\n    timeout: 3s\n", fastconf.WithDecodeFormat(fastconf.YAML))
	if yamlGot.Next.Next.Timeout != 3*time.Second {
		t.Fatalf("yaml recursive duration: %+v", yamlGot)
	}
}

func TestTypedHooks_MutuallyRecursiveTypes(t *testing.T) {
	got := loadYAML[mutualA](t, "b:\n  wait: 1s\n  a:\n    b:\n      wait: 2s\n")
	if got.B.Wait != time.Second || got.B.A.B.Wait != 2*time.Second {
		t.Fatalf("mutual recursion: %+v", got)
	}
}

func TestTypedHooks_DurationWithYAMLDecoder(t *testing.T) {
	type cfg struct {
		D time.Duration `yaml:"d"`
	}
	if got := loadYAML[cfg](t, "d: 1s\n", fastconf.WithDecodeFormat(fastconf.YAML)); got.D != time.Second {
		t.Fatalf("yaml decoder duration = %v", got.D)
	}
	type jsonCfg struct {
		D time.Duration `json:"d"`
	}
	if got := loadYAML[jsonCfg](t, "d: 1s\n"); got.D != time.Second {
		t.Fatalf("json decoder duration = %v", got.D)
	}
}

type durHolder struct {
	D time.Duration `json:"d" yaml:"d"`
}

type embedCfg struct {
	durHolder
	Ptr   *durHolder               `json:"ptr"`
	List  []durHolder              `json:"list"`
	PList []*durHolder             `json:"plist"`
	Map   map[string]durHolder     `json:"map"`
	Waits []time.Duration          `json:"waits"`
	ByKey map[string]time.Duration `json:"byKey"`
}

func TestTypedHooks_EmbeddedAndContainers(t *testing.T) {
	got := loadYAML[embedCfg](t, `
d: 1s
ptr: {d: 2s}
list: [{d: 3s}]
plist: [{d: 4s}]
map: {a: {d: 5s}}
waits: [6s, 7s]
byKey: {x: 8s}
`)
	if got.D != time.Second || got.Ptr.D != 2*time.Second || got.List[0].D != 3*time.Second ||
		got.PList[0].D != 4*time.Second || got.Map["a"].D != 5*time.Second ||
		got.Waits[1] != 7*time.Second || got.ByKey["x"] != 8*time.Second {
		t.Fatalf("containers not converted: %+v", got)
	}
}

func TestTypedHooks_YAMLInlineEmbed(t *testing.T) {
	type cfg struct {
		durHolder `yaml:",inline"`
		List      []durHolder `yaml:"list"`
	}
	got := loadYAML[cfg](t, "d: 1s\nlist: [{d: 2s}]\n", fastconf.WithDecodeFormat(fastconf.YAML))
	if got.D != time.Second || got.List[0].D != 2*time.Second {
		t.Fatalf("yaml inline: %+v", got)
	}
}

func TestURLHook_Load(t *testing.T) {
	type inner struct {
		U url.URL `json:"u" yaml:"u"`
	}
	type cfg struct {
		URL   *url.URL   `json:"url" yaml:"url"`
		Inner inner      `json:"inner" yaml:"inner"`
		List  []*url.URL `json:"list" yaml:"list"`
	}
	body := "url: https://example.com/a\ninner:\n  u: http://h:1/x\nlist: [https://b.example]\n"
	for _, opts := range [][]fastconf.Option{
		{fastconf.WithTypedHook(codec.URLHook{})},
		{fastconf.WithTypedHook(codec.URLHook{}), fastconf.WithDecodeFormat(fastconf.YAML)},
	} {
		got := loadYAML[cfg](t, body, opts...)
		if got.URL == nil || got.URL.String() != "https://example.com/a" {
			t.Fatalf("URL = %v", got.URL)
		}
		if got.Inner.U.Host != "h:1" || got.List[0].Host != "b.example" {
			t.Fatalf("nested URL: %+v", got)
		}
	}
}

func TestURLHook_InvalidURLFails(t *testing.T) {
	type cfg struct {
		URL *url.URL `json:"url"`
	}
	fs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("url: \"http://[::1\"\n")}}
	_, err := fastconf.Load[cfg](context.Background(), fastconf.WithFS(fs), fastconf.WithDir("conf.d"),
		fastconf.WithTypedHook(codec.URLHook{}))
	if err == nil {
		t.Fatal("expected url parse error")
	}
}
