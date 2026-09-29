// Dotted-path helpers for map[string]any trees: the single source of truth for
// read/write/delete-by-path operations used by transform, the env provider and the consul
// provider.

package confmap

import (
	"strings"
)

// Split splits a dotted path "a.b.c" into ["a", "b", "c"]. Empty path returns an empty slice
// (callers may treat that as "root").
func Split(dotted string) []string {
	if dotted == "" {
		return nil
	}
	return strings.Split(dotted, ".")
}

// Get returns the value at parts (or root[parts[0]][parts[1]]...) and whether it was found.
// Intermediate non-map values short-circuit to (nil,false).
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

// Set writes v at parts, creating intermediate maps as needed. Existing nil maps
// and non-map values along the path are silently overwritten by a fresh map
// (matches the established env/consul provider semantics).
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
		if !ok || next == nil {
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
