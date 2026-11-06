package fastconf

import (
	"go/ast"
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
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "doc.go" {
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

func TestExportedRootSymbolsDocumented(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil && !exportedReceiver(d.Recv) {
					continue // e.g. tomlNumber.MarshalTOML, policyAdapter.Name: not in go doc
				}
				if d.Name.IsExported() && d.Doc == nil {
					t.Errorf("%s: func %s has no doc comment", fset.Position(d.Pos()), d.Name)
				}
			case *ast.GenDecl:
				if d.Tok == token.IMPORT || d.Doc != nil {
					continue
				}
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() && s.Doc == nil {
							t.Errorf("%s: type %s has no doc comment", fset.Position(s.Pos()), s.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() && s.Doc == nil && s.Comment == nil {
								t.Errorf("%s: %s has no doc comment", fset.Position(n.Pos()), n)
							}
						}
					}
				}
			}
		}
	}
}
func exportedReceiver(recv *ast.FieldList) bool {
	e := recv.List[0].Type
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.IsExported()
		default:
			return false
		}
	}
}
