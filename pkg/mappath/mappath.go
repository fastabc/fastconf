// Package mappath provides dotted-path helpers for map[string]any trees.
// It is the single source of truth for read/write/delete-by-path
// operations previously duplicated in transform/, provider/env, and
// providers/consul.
package mappath

import (
	"strings"

	"github.com/fastabc/fastconf/pkg/typed"
)

// Split splits a dotted path "a.b.c" into ["a", "b", "c"]. Empty path
// returns an empty slice (callers may treat that as "root").
func Split(dotted string) []string {
	if dotted == "" {
		return nil
	}
	return strings.Split(dotted, ".")
}

// Get returns the value at parts (or root[parts[0]][parts[1]]...) and
// whether it was found. Intermediate non-map values short-circuit to
// (nil,false).
func Get(root map[string]any, parts ...string) (any, bool) {
	if root == nil || len(parts) == 0 {
		return nil, false
	}
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// GetDotted is the convenience wrapper for "a.b.c" callers.
func GetDotted(root map[string]any, dotted string) (any, bool) {
	return Get(root, Split(dotted)...)
}

// Set writes v at parts, creating intermediate maps as needed. Existing
// non-map values along the path are silently overwritten by a fresh
// map (matches the legacy env/consul behavior).
func Set(root map[string]any, parts []string, v any) {
	if root == nil || len(parts) == 0 {
		return
	}
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = v
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// SetDotted is the convenience wrapper for "a.b.c" callers.
func SetDotted(root map[string]any, dotted string, v any) {
	Set(root, Split(dotted), v)
}

// Delete removes the leaf at parts; missing paths are silently ignored.
func Delete(root map[string]any, parts []string) {
	if root == nil || len(parts) == 0 {
		return
	}
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			delete(cur, p)
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

// DeleteDotted is the convenience wrapper for "a.b.c" callers.
func DeleteDotted(root map[string]any, dotted string) {
	Delete(root, Split(dotted))
}

// LabelOptions controls how ExpandLabels reshapes flat labels into a nested
// map[string]any.
type LabelOptions struct {
	// Prefix, when non-empty, restricts expansion to labels whose key starts
	// with this prefix (e.g. "routing."). Labels lacking the prefix are
	// silently skipped.
	Prefix string
	// StripPrefix removes Prefix from each key before expansion. Has no
	// effect when Prefix is empty.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".". Use
	// Separators (plural) when more than one delimiter is in play (e.g.
	// K8s recommended labels with both "/" and "."). When both fields are
	// set, Separators wins.
	Separator string
	// Separators is the ordered list of delimiters applied to each key.
	// Splits happen left-to-right: the input is first split by Separators[0],
	// then each segment is split by Separators[1], and so on. This lets
	// K8s-style "app.kubernetes.io/name" decompose coherently — e.g.
	// {"/", "."} produces parts ["app", "kubernetes", "io", "name"].
	// When empty, Separator (singular) is used; when both are empty, "."
	// is the fallback.
	Separators []string
	// Coerce, when true, converts "true" / "false" / int-like / float-like
	// values into their typed forms (matching pkg/provider env coercion).
	// Default false: values are kept verbatim as strings.
	Coerce bool
}

// ExpandLabels reshapes a flat list / map of "dotted.key=value" labels into a
// nested map[string]any. Accepted input shapes:
//
//   - []string{"a.b=1", "a.c=2"}            — Compose / docker CLI form
//   - []any{"a.b=1", "a.c=2"}               — YAML-decoded form
//   - map[string]string{"a.b":"1","a.c":"2"}— Docker engine / K8s annotation form
//   - map[string]any{"a.b":"1","a.c":"2"}   — already-decoded YAML map
//
// Malformed entries (no '=' separator, empty key after prefix trim) are
// silently dropped. The result is a freshly allocated tree; callers may merge
// it into an existing root via pkg/merger.Deep.
func ExpandLabels(input any, opts LabelOptions) map[string]any {
	seps := resolveSeparators(opts)
	out := map[string]any{}
	for _, pair := range NormalizeLabelInput(input) {
		k := pair.Key
		if opts.Prefix != "" {
			if !strings.HasPrefix(k, opts.Prefix) {
				continue
			}
			if opts.StripPrefix {
				k = strings.TrimPrefix(k, opts.Prefix)
				// Strip a leading delimiter introduced by the prefix
				// boundary, regardless of which separator it is.
				for _, s := range seps {
					if s != "" && strings.HasPrefix(k, s) {
						k = strings.TrimPrefix(k, s)
						break
					}
				}
			}
		}
		if k == "" {
			continue
		}
		parts := splitMulti(k, seps)
		var value any = pair.Value
		if opts.Coerce {
			value = coerceLabelValue(pair.Value)
		}
		Set(out, parts, value)
	}
	return out
}

// resolveSeparators returns the effective separator list, honoring
// Separators when non-empty, then Separator (singular), then the
// fallback ".".
func resolveSeparators(opts LabelOptions) []string {
	if len(opts.Separators) > 0 {
		seps := make([]string, 0, len(opts.Separators))
		for _, s := range opts.Separators {
			if s != "" {
				seps = append(seps, s)
			}
		}
		if len(seps) > 0 {
			return seps
		}
	}
	if opts.Separator != "" {
		return []string{opts.Separator}
	}
	return []string{"."}
}

// splitMulti splits s by seps[0], then each resulting segment by
// seps[1], and so on. Empty segments are dropped so back-to-back
// delimiters do not create empty path components.
func splitMulti(s string, seps []string) []string {
	parts := []string{s}
	for _, sep := range seps {
		next := make([]string, 0, len(parts))
		for _, p := range parts {
			for _, sub := range strings.Split(p, sep) {
				if sub != "" {
					next = append(next, sub)
				}
			}
		}
		parts = next
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

// coerceLabelValue mirrors pkg/provider env coercion: bool / int64 / float64
// / string in that order. Case-sensitive and whitespace-preserving — labels
// that have already been canonicalized by ExpandLabels do not need either
// rung. Use pkg/typed.Coerce directly for callers that need a different
// policy.
func coerceLabelValue(s string) any {
	return typed.Coerce(s, typed.CoerceOptions{})
}
