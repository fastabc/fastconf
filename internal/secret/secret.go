// Package secret implements the redaction and resolver primitives that
// back fastconf's `fastconf:"secret"` tag and WithSecretResolver hook.
// The root fastconf package keeps thin facades; the actual reflection
// walk, path expansion, and resolver tree walk live here so the root
// can stay public-API-only.
package secret

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// Redactor turns a sensitive value into its display form. It receives
// the dotted path and the raw decoded value, and returns whatever should
// be surfaced in dumps, logs and CLI output.
type Redactor func(path string, value any) any

// DefaultRedactor replaces the value with "***REDACTED***".
func DefaultRedactor(_ string, _ any) any { return "***REDACTED***" }

var pathsCache = typeinfo.NewCache[[]string]()

// Paths returns the cached, sorted list of dotted paths whose fields
// carry the `fastconf:"secret"` marker. The walk understands json/yaml
// tags for naming and recurses into anonymous embeds, named struct
// fields and pointer-to-struct.
func Paths(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return pathsCache.GetOrCompute(t, func() []string {
		seen := map[string]struct{}{}
		typeinfo.Walk(t, typeinfo.WalkFunc(func(path string, _ []int, f reflect.StructField, _ *reflect.Type) bool {
			if HasTag(f.Tag.Get("fastconf")) {
				seen[path] = struct{}{}
			}
			return true
		}))
		out := make([]string, 0, len(seen))
		for p := range seen {
			out = append(out, p)
		}
		sort.Strings(out)
		return out
	})
}

// HasTag reports whether tag contains the bare "secret" token in a
// comma-separated `fastconf` struct tag.
func HasTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, p := range strings.Split(tag, ",") {
		if strings.TrimSpace(p) == "secret" {
			return true
		}
	}
	return false
}

// Apply walks m in-place, replacing every value at a path in paths via
// redactor. Returns m for chaining convenience.
func Apply(m map[string]any, paths []string, redactor Redactor) map[string]any {
	if redactor == nil {
		redactor = DefaultRedactor
	}
	for _, p := range paths {
		applyOne(m, strings.Split(p, "."), p, redactor)
	}
	return m
}

func applyOne(m map[string]any, parts []string, path string, redactor Redactor) {
	if len(parts) == 0 || m == nil {
		return
	}
	head := parts[0]
	if len(parts) == 1 {
		if v, ok := m[head]; ok {
			m[head] = redactor(path, v)
		}
		return
	}
	switch parts[1] {
	case "[]":
		arr, ok := m[head].([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			if child, ok := item.(map[string]any); ok {
				applyOne(child, parts[2:], path, redactor)
			}
		}
	case "{}":
		mm, ok := m[head].(map[string]any)
		if !ok {
			return
		}
		for _, v := range mm {
			if child, ok := v.(map[string]any); ok {
				applyOne(child, parts[2:], path, redactor)
			}
		}
	default:
		child, ok := m[head].(map[string]any)
		if !ok {
			return
		}
		applyOne(child, parts[1:], path, redactor)
	}
}

// Ref identifies one opaque secret reference recognised by a Resolver.
// Scheme is the lookup namespace ("sops", "age", "vault", "kms", ...);
// Body is the scheme-specific payload.
type Ref struct {
	Scheme string
	Body   string
}

// Resolver decrypts opaque secret references that appear in the merged map.
// Recognize is called on every leaf string; Resolve runs once per recognised
// reference per reload, on the single reload goroutine.
type Resolver interface {
	Recognize(v string) (Ref, bool)
	Resolve(ctx context.Context, ref Ref) (string, error)
}

// ResolverFunc adapts a pair of functions into a Resolver.
type ResolverFunc struct {
	RecognizeFn func(string) (Ref, bool)
	ResolveFn   func(context.Context, Ref) (string, error)
}

// Recognize implements Resolver.
func (f ResolverFunc) Recognize(v string) (Ref, bool) {
	if f.RecognizeFn == nil {
		return Ref{}, false
	}
	return f.RecognizeFn(v)
}

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(ctx context.Context, ref Ref) (string, error) {
	if f.ResolveFn == nil {
		return "", errors.New("fastconf: SecretResolver has no Resolve function")
	}
	return f.ResolveFn(ctx, ref)
}

// MaxWalkDepth caps the depth WalkLeaves descends to defeat YAML anchor cycles.
const MaxWalkDepth = 256

// WalkLeaves traverses node depth-first and invokes fn on every string
// leaf. fn returns the (possibly rewritten) value plus a bool indicating
// whether the rewrite should be applied in place.
func WalkLeaves(node any, prefix string, fn func(path, v string) (string, bool)) {
	walkLeavesDepth(node, prefix, fn, 0)
}

func walkLeavesDepth(node any, prefix string, fn func(path, v string) (string, bool), depth int) {
	if depth > MaxWalkDepth {
		return
	}
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			full := k
			if prefix != "" {
				full = prefix + "." + k
			}
			if s, ok := v.(string); ok {
				if newV, replaced := fn(full, s); replaced {
					n[k] = newV
				}
				continue
			}
			walkLeavesDepth(v, full, fn, depth+1)
		}
	case []any:
		for i, v := range n {
			full := fmt.Sprintf("%s.[%d]", prefix, i)
			if s, ok := v.(string); ok {
				if newV, replaced := fn(full, s); replaced {
					n[i] = newV
				}
				continue
			}
			walkLeavesDepth(v, full, fn, depth+1)
		}
	}
}
