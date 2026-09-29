package secret

import (
	"strconv"
	"strings"
)

// ApplyPatterns masks every value in tree whose dotted path matches one of
// patterns. Segments are separated by "."; "*" matches exactly one segment
// and "**" matches zero or more. List elements are addressed by index. A
// matched container is masked as a whole. Unlike ApplyJSON it needs no
// struct tags, so it covers map-typed configurations.
func ApplyPatterns(tree map[string]any, patterns []string, redactor Redactor) map[string]any {
	if len(patterns) == 0 {
		return tree
	}
	if redactor == nil {
		redactor = DefaultRedactor
	}
	compiled := make([][]string, len(patterns))
	for i, p := range patterns {
		compiled[i] = strings.Split(p, ".")
	}
	redactPatterns(tree, nil, compiled, redactor)
	return tree
}

// PatternsMatchValue checks a provenance value and its ancestors/descendants
// without relying on paths still present in the published snapshot.
func PatternsMatchValue(value any, path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	segments := strings.Split(path, ".")
	compiled := make([][]string, len(patterns))
	for i, p := range patterns {
		compiled[i] = strings.Split(p, ".")
		for n := 1; n <= len(segments); n++ {
			if matchSegments(compiled[i], segments[:n]) {
				return true
			}
		}
	}
	var hit bool
	redactPatterns(value, segments, compiled, func(_ string, v any) any {
		hit = true
		return v
	})
	return hit
}

func redactPatterns(node any, path []string, patterns [][]string, redact Redactor) {
	visit := func(seg string, value any, set func(any)) {
		p := append(path, seg)
		for _, pat := range patterns {
			if matchSegments(pat, p) {
				set(redact(strings.Join(p, "."), value))
				return
			}
		}
		redactPatterns(value, p, patterns, redact)
	}
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			visit(k, v, func(r any) { n[k] = r })
		}
	case []any:
		for i, v := range n {
			visit(strconv.Itoa(i), v, func(r any) { n[i] = r })
		}
	}
}

func matchSegments(pat, path []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(path); i++ {
				if matchSegments(pat[1:], path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 || (pat[0] != "*" && pat[0] != path[0]) {
			return false
		}
		pat, path = pat[1:], path[1:]
	}
	return len(path) == 0
}
