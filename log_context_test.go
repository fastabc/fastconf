package fastconf

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Library logs must carry the caller's or the manager's context so slog
// handlers can attach trace and request values. Linters such as sloglint do not
// catch this: LogAttrs already takes a context, so context.Background() passes.
func TestLogAttrsUsesContext(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "LogAttrs" && isDetachedContext(call.Args[0]) {
				t.Errorf("%s: LogAttrs with a detached context; pass the caller's or the manager's context",
					fset.Position(call.Pos()))
			}
			return true
		})
	}
}

// isDetachedContext reports a context.Background() or context.TODO() call.
func isDetachedContext(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context" && (sel.Sel.Name == "Background" || sel.Sel.Name == "TODO")
}
