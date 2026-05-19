package fastconf_test

// Per H6.3 of docs/plans/v0.18.0-prerelease-audit.md: doc.go is the
// godoc landing page and must list the canonical "where do I start"
// surface so newcomers do not have to scan the entire alphabetised
// symbol index. This test fails if any of those symbols disappear from
// the package-level comment block.

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestDocLanding_ListsRecommendedExports(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "doc.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse doc.go: %v", err)
	}
	if f.Doc == nil {
		t.Fatal("doc.go: package-level godoc comment block is missing")
	}
	doc := f.Doc.Text()
	required := []string{
		"New", "MustNew",
		"PresetK8s", "PresetSidecar", "PresetTesting", "PresetHierarchical",
		"WithProfile", "WithWatch", "WithCoalesce",
		"WithProvider", "WithMultiAxisOverlays",
		"Subscribe", "Plan",
	}
	for _, sym := range required {
		if !strings.Contains(doc, sym) {
			t.Errorf("doc.go landing block missing recommended export %q", sym)
		}
	}
}
