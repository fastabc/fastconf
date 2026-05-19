package manager

import (
	"context"
	"testing"
	"testing/fstest"

	iopts "github.com/fastabc/fastconf/internal/options"
)

type yamlOnly struct {
	A string `yaml:"alpha"`
	B int    `yaml:"beta"`
}

type jsonAndYAML struct {
	A string `json:"alpha" yaml:"alpha"`
	B int    `json:"beta" yaml:"beta"`
}

func TestCodecBridge_DefaultJSON_RequiresJSONTags(t *testing.T) {
	mfs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("alpha: hi\nbeta: 42\n")}}
	cfg, err := New[jsonAndYAML](context.Background(),
		func(o *iopts.Options) {
			o.FS = mfs
			o.Dir = "conf.d"
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	if g := cfg.Get(); g.A != "hi" || g.B != 42 {
		t.Fatalf("got %+v", g)
	}
}

func TestCodecBridge_DefaultJSON_DoesNotUseYAMLOnlyTags(t *testing.T) {
	mfs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("alpha: hi\nbeta: 42\n")}}
	cfg, err := New[yamlOnly](context.Background(),
		func(o *iopts.Options) {
			o.FS = mfs
			o.Dir = "conf.d"
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	if g := cfg.Get(); g.A != "" || g.B != 0 {
		t.Fatalf("got %+v", g)
	}
}

func TestCodecBridge_YAML_HonoursYAMLOnlyTags(t *testing.T) {
	mfs := fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("alpha: hi\nbeta: 42\n")}}
	cfg, err := New[yamlOnly](context.Background(),
		func(o *iopts.Options) {
			o.FS = mfs
			o.Dir = "conf.d"
		},
		func(o *iopts.Options) { o.CodecBridge = iopts.BridgeYAML },
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	if g := cfg.Get(); g.A != "hi" || g.B != 42 {
		t.Fatalf("got %+v", g)
	}
}

type hashStruct struct {
	B string `json:"b"`
	A int    `json:"a"`
}

func TestCanonicalHashBytes_StructUsesCanonicalStructJSON(t *testing.T) {
	cfg := &hashStruct{B: "hello", A: 1}
	mergedJSON := []byte(`{"a":1,"b":"hello","extra":true}`)

	got, err := canonicalHashBytes(mergedJSON, cfg, iopts.BridgeJSON)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalHash(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("hash mismatch: got %x want %x", got, want)
	}
}
