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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"

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

// Defaults returns a Transformer that recursively merges the supplied
// values into the tree, only filling keys that are missing.
func Defaults(values map[string]any) Transformer {
	return TransformerFunc{
		NameStr: "Defaults",
		Fn: func(root map[string]any) error {
			mergeDefaults(root, values)
			return nil
		},
	}
}

// SetIfAbsent sets a single dotted-path key only when it has no value.
func SetIfAbsent(path string, value any) Transformer {
	return TransformerFunc{
		NameStr: "SetIfAbsent(" + path + ")",
		Fn: func(root map[string]any) error {
			if _, ok := getPath(root, path); ok {
				return nil
			}
			setPath(root, path, value)
			return nil
		},
	}
}

func mergeDefaults(dst, src map[string]any) {
	for k, sv := range src {
		dv, exists := dst[k]
		if !exists {
			dst[k] = cloneAny(sv)
			continue
		}
		dm, dok := dv.(map[string]any)
		sm, sok := sv.(map[string]any)
		if dok && sok {
			mergeDefaults(dm, sm)
		}
	}
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = cloneAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = cloneAny(vv)
		}
		return out
	default:
		return v
	}
}

// envPattern matches ${VAR} or ${VAR:-default}. Bare $VAR is intentionally
// NOT matched to avoid clashing with bcrypt-style password fields.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// EnvSubst returns a Transformer that walks every string value in the
// tree and substitutes occurrences of ${VAR} or ${VAR:-default}.
func EnvSubst() Transformer { return EnvSubstWith(os.Getenv) }

// EnvSubstWith is like EnvSubst but reads variables through the
// supplied lookup function.
func EnvSubstWith(lookup func(string) string) Transformer {
	return TransformerFunc{
		NameStr: "EnvSubst",
		Fn: func(root map[string]any) error {
			walkStrings(root, func(s string) string {
				return envPattern.ReplaceAllStringFunc(s, func(match string) string {
					m := envPattern.FindStringSubmatch(match)
					name, def := m[1], m[2]
					if v := lookup(name); v != "" {
						return v
					}
					return def
				})
			})
			return nil
		},
	}
}

func walkStrings(node any, fn func(string) string) any {
	switch v := node.(type) {
	case map[string]any:
		for k, vv := range v {
			v[k] = walkStrings(vv, fn)
		}
		return v
	case []any:
		for i, vv := range v {
			v[i] = walkStrings(vv, fn)
		}
		return v
	case string:
		return fn(v)
	default:
		return v
	}
}

// DeletePaths returns a Transformer that removes the specified dotted-path
// keys from the tree. Missing paths are silently ignored.
func DeletePaths(paths ...string) Transformer {
	return TransformerFunc{
		NameStr: "DeletePaths",
		Fn: func(root map[string]any) error {
			for _, p := range paths {
				deletePath(root, p)
			}
			return nil
		},
	}
}

func deletePath(root map[string]any, path string) {
	mappath.DeleteDotted(root, path)
}

// Aliases returns a Transformer that rewrites legacy keys to their new
// home. If the target path already has a value the new world wins and
// the alias is dropped.
func Aliases(mapping map[string]string) Transformer {
	return TransformerFunc{
		NameStr: "Aliases",
		Fn: func(root map[string]any) error {
			for from, to := range mapping {
				v, ok := getPath(root, from)
				if !ok {
					continue
				}
				if _, exists := getPath(root, to); !exists {
					setPath(root, to, v)
				}
				deletePath(root, from)
			}
			return nil
		},
	}
}

func getPath(root map[string]any, path string) (any, bool) {
	return mappath.GetDotted(root, path)
}

func setPath(root map[string]any, path string, value any) {
	mappath.SetDotted(root, path, value)
}

// Wrap turns a built-in error into a wrapped ErrTransform with the
// transformer name attached.
func Wrap(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %v", ErrTransform, name, err)
}

// MergeByKey returns a Transformer that merges an array of maps by a key
// field, enabling overlay files to modify individual entries without
// replacing the entire array.
//
// The array at the given dotted path is expected to contain map[string]any
// items each with a distinguishing field (keyField). Items from the
// current array are merged on top of items from the same-keyed base item.
// If a key appears only once the item is kept as-is.
//
// This is useful for protocol-block patterns where each entry has an
// identifier:
//
//	listeners:
//	  - name: http
//	    port: 80
//	  - name: https
//	    port: 443
//
// A subsequent overlay with only the https entry will update port 443
// without discarding the http entry.
func MergeByKey(dotPath, keyField string) Transformer {
	name := fmt.Sprintf("MergeByKey(%s,%s)", dotPath, keyField)
	return TransformerFunc{
		NameStr: name,
		Fn: func(root map[string]any) error {
			raw, ok := getPath(root, dotPath)
			if !ok {
				return nil
			}
			items, ok := raw.([]any)
			if !ok {
				return nil
			}
			// Index by key: last-one-wins for duplicates within this array.
			type entry struct {
				idx  int
				item map[string]any
			}
			ordered := make([]string, 0, len(items))
			byKey := make(map[string]entry, len(items))
			for _, raw := range items {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				kv, ok := m[keyField]
				if !ok {
					continue
				}
				key := fmt.Sprint(kv)
				if existing, exists := byKey[key]; exists {
					// Merge new map on top of existing map.
					merged := make(map[string]any, len(existing.item))
					for k, v := range existing.item {
						merged[k] = v
					}
					for k, v := range m {
						merged[k] = v
					}
					byKey[key] = entry{idx: existing.idx, item: merged}
				} else {
					byKey[key] = entry{idx: len(ordered), item: m}
					ordered = append(ordered, key)
				}
			}
			merged := make([]any, 0, len(ordered))
			for _, key := range ordered {
				merged = append(merged, byKey[key].item)
			}
			setPath(root, dotPath, merged)
			return nil
		},
	}
}

// RawCapture is a Transformer that snapshots one or more dotted-path values
// as json.RawMessage after the merge phase but before decoding into *T.
// This is the recommended solution for map[string][]json.RawMessage protocol
// blocks: register a RawCapture transformer, decode *T normally, then call
// rawCapture.Get(path) to retrieve the opaque bytes.
//
// RawCapture is safe for concurrent reads (Get/All) after a reload.
//
// Usage:
//
//	rc := transform.CaptureRaw("listeners", "upstreams")
//	mgr, _ := fastconf.New[Config](ctx,
//	    fastconf.WithTransformers(rc),
//	    // ... other options
//	)
//	cfg := mgr.Get()
//	raw, _ := rc.Get("listeners")  // raw is json.RawMessage
type RawCapture struct {
	paths  []string
	mu     sync.RWMutex
	values map[string]json.RawMessage
}

// CaptureRaw returns a new RawCapture transformer that will snapshot the
// values at the given dotted paths on every reload.
func CaptureRaw(paths ...string) *RawCapture {
	return &RawCapture{
		paths:  paths,
		values: make(map[string]json.RawMessage),
	}
}

// Name implements Transformer.
func (r *RawCapture) Name() string { return "CaptureRaw(" + fmt.Sprint(r.paths) + ")" }

// Transform implements Transformer. It snapshots the current value at each
// registered path as JSON bytes. Missing paths are silently skipped and their
// previous captured value is cleared.
func (r *RawCapture) Transform(root map[string]any) error {
	newValues := make(map[string]json.RawMessage, len(r.paths))
	for _, p := range r.paths {
		v, ok := getPath(root, p)
		if !ok {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%w: CaptureRaw path %q: %v", ErrTransform, p, err)
		}
		newValues[p] = b
	}
	r.mu.Lock()
	r.values = newValues
	r.mu.Unlock()
	return nil
}

// Get returns the most recently captured JSON bytes for the given path.
// Returns false if the path was not registered or was missing at last reload.
func (r *RawCapture) Get(path string) (json.RawMessage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[path]
	return v, ok
}

// All returns a snapshot of all captured path → JSON bytes. The returned map
// is a copy and is safe for the caller to retain.
func (r *RawCapture) All() map[string]json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out
}

