// Package secret implements the redaction and resolver primitives that
// back fastconf's `fc:"secret"` tag and WithSecretResolver hook.
// The root fastconf package keeps thin facades; the redaction walk and
// resolver tree walk live here so the root can stay public-API-only.
package secret

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Redactor turns a sensitive value into its display form. It receives
// the dotted path and the raw decoded value, and returns whatever should
// be surfaced in dumps, logs and CLI output.
type Redactor func(path string, value any) any

// DefaultRedactor replaces the value with "***REDACTED***".
func DefaultRedactor(_ string, _ any) any { return "***REDACTED***" }

// HasTag reports whether tag contains the bare "secret" token in a
// comma-separated `fc` struct tag.
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

// maxWalkDepth caps the depth WalkLeaves descends to defeat YAML anchor cycles.
const maxWalkDepth = 256

// WalkLeaves traverses node depth-first and invokes fn on every string
// leaf. fn returns the (possibly rewritten) value plus a bool indicating
// whether the rewrite should be applied in place.
func WalkLeaves(node any, prefix string, fn func(path, v string) (string, bool)) {
	walkLeavesDepth(node, prefix, fn, 0)
}

func walkLeavesDepth(node any, prefix string, fn func(path, v string) (string, bool), depth int) {
	if depth > maxWalkDepth {
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
			full := joinPath(prefix, strconv.Itoa(i))
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
