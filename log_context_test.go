package fastconf

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

type logCtxKey struct{}

// ctxValueHandler records the logCtxKey value carried by each record's context.
type ctxValueHandler struct {
	mu   sync.Mutex
	seen map[string][]any
}

func (h *ctxValueHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *ctxValueHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen[r.Message] = append(h.seen[r.Message], ctx.Value(logCtxKey{}))
	return nil
}

func (h *ctxValueHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *ctxValueHandler) WithGroup(string) slog.Handler      { return h }

func (h *ctxValueHandler) values(msg string) []any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.seen[msg])
}

// File-triggered reloads run on the manager's lifetime, like provider, notify
// and resync triggers, so their logs keep the values of New's context.
func TestFileWatchReloadLogsKeepManagerContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base", "00.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &ctxValueHandler{seen: map[string][]any{}}
	ctx := context.WithValue(context.Background(), logCtxKey{}, "trace-1")
	causes := make(causeRecorder, 32)
	m, err := New[snapshotConfig](ctx, WithDir(dir), WithLogger(slog.New(h)),
		WithWatch(Watch{Quiet: 5 * time.Millisecond, MaxLag: 20 * time.Millisecond}), WithObserver(causes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := os.WriteFile(path, []byte("name: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	awaitTrigger(t, causes, TriggerFile, reasonPrefix("fs:"))
	got := h.values("fastconf reload start")
	if len(got) < 2 {
		t.Fatalf("reload start logged %d times; want initial and file-triggered", len(got))
	}
	for i, v := range got {
		if v != "trace-1" {
			t.Fatalf("reload start #%d context value = %v; want trace-1", i, v)
		}
	}
}
