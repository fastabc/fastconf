package fastconf

// SecretResolver hook lets operators wire SOPS / age / Vault transit /
// AWS KMS into the reload pipeline so encrypted-at-rest values in YAML
// can be decrypted before the document is decoded into *T.
//
// The hook runs after stageTransform and before stageDecode. A failure
// aborts the reload and preserves the previously committed *State[T],
// matching the failure-safe contract of every other stage.

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastabc/fastconf/pkg/mappath"
)

// SecretRef identifies one opaque secret reference recognised by a
// SecretResolver. Scheme is the lookup namespace ("sops", "age",
// "vault", "kms", "fastconf-enc", ...); Body is the scheme-specific
// payload (cipher text, kms arn, file pointer).
type SecretRef struct {
	Scheme string
	Body   string
}

// SecretResolver decrypts opaque secret references that appear in the
// merged map. Implementations may call SOPS, Vault transit, AWS KMS,
// age, or a local keyring.
//
// Recognize is called on every leaf string in the merged map; returning
// (SecretRef{}, false) leaves the value untouched. Recognize MUST be
// pure and side-effect free — the framework may call it many times per
// reload.
//
// Resolve is called once per recognised reference per reload, on the
// single reload goroutine, with the original ctx. Returning a non-nil
// error aborts the reload (failure-safe).
type SecretResolver interface {
	Recognize(v string) (SecretRef, bool)
	Resolve(ctx context.Context, ref SecretRef) (string, error)
}

// SecretResolverFunc adapts a pair of functions into a SecretResolver.
type SecretResolverFunc struct {
	RecognizeFn func(string) (SecretRef, bool)
	ResolveFn   func(context.Context, SecretRef) (string, error)
}

// Recognize implements SecretResolver.
func (f SecretResolverFunc) Recognize(v string) (SecretRef, bool) {
	if f.RecognizeFn == nil {
		return SecretRef{}, false
	}
	return f.RecognizeFn(v)
}

// Resolve implements SecretResolver.
func (f SecretResolverFunc) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	if f.ResolveFn == nil {
		return "", errors.New("fastconf: SecretResolver has no Resolve function")
	}
	return f.ResolveFn(ctx, ref)
}

// WithSecretResolver installs a resolver that walks the merged map
// before decode, replacing every recognised reference with its
// plaintext. Decryption errors abort the reload (failure-safe).
func WithSecretResolver(r SecretResolver) Option {
	return func(o *options) { o.secretResolver = r }
}

// runSecretResolve walks pc.merged depth-first, recognising and replacing
// every leaf string the configured resolver claims. Records each hit on
// the provenance chain (LayerSecret) so callers can audit "where did
// this value come from" without leaking the cipher text.
func runSecretResolve[T any](ctx context.Context, m *Manager[T], pc *pipelineCtx[T]) error {
	r := m.opts.secretResolver
	if r == nil {
		return nil
	}
	var firstErr error
	walkSecretLeaves(pc.merged, "", func(path string, v string) (string, bool) {
		if firstErr != nil {
			return v, false
		}
		ref, ok := r.Recognize(v)
		if !ok {
			return v, false
		}
		plain, err := r.Resolve(ctx, ref)
		if err != nil {
			firstErr = fmt.Errorf("%w: secret %s@%s: %v", ErrTransform, ref.Scheme, path, err)
			return v, false
		}
		if pc.origins != nil {
			pc.origins.record(path, SourceRef{
				Kind:     LayerSecret,
				Path:     "secret://" + ref.Scheme,
				Priority: 9500,
			})
		}
		return plain, true
	})
	return firstErr
}

// walkSecretLeaves traverses the merged map and rewrites every string
// leaf via fn. Maps and slices are descended; non-string scalars are
// left in place. The walk is depth-limited to defeat YAML anchor cycles.
const maxSecretWalkDepth = 256

func walkSecretLeaves(node any, prefix string, fn func(path, v string) (string, bool)) {
	walkSecretLeavesDepth(node, prefix, fn, 0)
}

func walkSecretLeavesDepth(node any, prefix string, fn func(path, v string) (string, bool), depth int) {
	if depth > maxSecretWalkDepth {
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
			walkSecretLeavesDepth(v, full, fn, depth+1)
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
			walkSecretLeavesDepth(v, full, fn, depth+1)
		}
	}
}

// Guard against unused-import warnings if the file is compiled without
// touching mappath; mappath is referenced for godoc readers wanting to
// understand the path encoding.
var _ = mappath.GetDotted
