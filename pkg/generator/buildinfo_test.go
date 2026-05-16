package generator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fastabc/fastconf/pkg/generator"
	"github.com/fastabc/fastconf/pkg/mappath"
)

func TestBuildInfo_FlatKeysBecomeNested(t *testing.T) {
	b := &generator.BuildInfo{Keys: map[string]string{
		"app.version": "1.2.3",
		"app.commit":  "abc",
	}}
	got, err := b.Generate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Codec != "json" {
		t.Fatalf("unexpected: %+v", got)
	}
	var tree map[string]any
	if err := json.Unmarshal(got[0].Data, &tree); err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(tree, "app.version"); v != "1.2.3" {
		t.Fatalf("version got %v", v)
	}
	if v, _ := mappath.GetDotted(tree, "app.commit"); v != "abc" {
		t.Fatalf("commit got %v", v)
	}
}

func TestBuildInfo_EmptyKeysReturnsNoSources(t *testing.T) {
	got, err := (&generator.BuildInfo{}).Generate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil sources, got %d", len(got))
	}
}

func TestBuildInfo_DefaultName(t *testing.T) {
	if (&generator.BuildInfo{}).Name() != "buildinfo" {
		t.Fail()
	}
	if (&generator.BuildInfo{NameStr: "custom"}).Name() != "custom" {
		t.Fail()
	}
}
