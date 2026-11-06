package secret

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestPackageDocOnlyInDocGo(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "secret.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments|parser.PackageClauseOnly)
		if err != nil {
			t.Fatal(err)
		}
		if f.Doc != nil {
			t.Errorf("%s: comment attached to package clause leaks into go doc: %q", name, f.Doc.Text())
		}
	}
}
