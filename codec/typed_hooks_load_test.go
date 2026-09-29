package codec_test

import (
	"context"
	"net/url"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/codec"
)

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
	yamlGot := loadYAML[selfNode](t, "next:\n  next:\n    timeout: 3s\n", fastconf.WithDecoder(fastconf.YAML))
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
	if got := loadYAML[cfg](t, "d: 1s\n", fastconf.WithDecoder(fastconf.YAML)); got.D != time.Second {
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
	got := loadYAML[cfg](t, "d: 1s\nlist: [{d: 2s}]\n", fastconf.WithDecoder(fastconf.YAML))
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
		{fastconf.WithTypedHook(codec.URLHook{}), fastconf.WithDecoder(fastconf.YAML)},
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
