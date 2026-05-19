// Package transform provides composable, post-merge / pre-decode
// transformations on the merged configuration tree.
//
// FastConf's reload pipeline is conceptually:
//
//	discover → decode → merge/patch → [TRANSFORMERS] → decodeInto(*T)
//	                                                  → validate → publish
//
// A Transformer is a pure function `func(map[string]any) error` that may
// mutate the in-place merged map before it is decoded into the user's
// strongly-typed struct. Built-in transformers cover the most common
// cases (defaults, env interpolation, key aliasing, deletion). Users
// can also write their own.
//
// Transformers run in declaration order and are wired via
// `fastconf.WithTransformers(...)`. Failures abort the reload and the
// previously committed state is preserved (same guarantee as every
// other stage).
//
// Design notes:
//   - Transformers operate on `map[string]any` rather than `*T` so
//     they remain decoupled from the user type and can be reused across
//     multiple Manager[T] instances.
//   - Path syntax used by helpers below is dotted: "a.b.c". Numeric
//     indices into slices are NOT supported (config trees are usually
//     small maps; complex array surgery belongs in RFC 6902 patches).
//   - All helpers tolerate a nil root map (treated as empty); they
//     never panic on missing intermediate nodes.
package transform

import (
	"errors"
	"fmt"

	"github.com/fastabc/fastconf/pkg/mappath"
)

// Transformer mutates the merged configuration tree. Returning an
// error aborts the reload. Implementations MUST be safe to call
// concurrently with reads of unrelated Manager instances but are
// guaranteed to be invoked serially within a single reload.
type Transformer interface {
	Transform(root map[string]any) error
	Name() string
}

// TransformerFunc adapts a plain function to the Transformer interface.
type TransformerFunc struct {
	NameStr string
	Fn      func(map[string]any) error
}

func (t TransformerFunc) Transform(root map[string]any) error { return t.Fn(root) }
func (t TransformerFunc) Name() string                        { return t.NameStr }

// ErrTransform is returned wrapped by built-in transformers on failure.
var ErrTransform = errors.New("fastconf/internal/transform")

// Wrap turns a built-in error into a wrapped ErrTransform with the
// transformer name attached.
func Wrap(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %v", ErrTransform, name, err)
}

// getPath and setPath are thin wrappers over mappath used by multiple
// built-in transformers (SetIfAbsent, Aliases, MergeByKey, RawCapture).
func getPath(root map[string]any, path string) (any, bool) {
	return mappath.GetDotted(root, path)
}

func setPath(root map[string]any, path string, value any) {
	mappath.SetDotted(root, path, value)
}

// deletePath is used by DeletePaths and Aliases.
func deletePath(root map[string]any, path string) {
	mappath.DeleteDotted(root, path)
}
