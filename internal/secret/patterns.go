package secret

import (
	"strconv"
	"strings"
)

// patternPlan keeps distinct rules for real tree segments and flattened
// provenance paths, where literal dots in map keys are ambiguous.
type patternPlan struct{ tree, provenance [][]string }

func compilePatterns(patterns []string) patternPlan {
	var out patternPlan
	for _, pattern := range patterns {
		segments := strings.Split(pattern, ".")
		out.tree = append(out.tree, segments)
		var conservative []string
		for _, segment := range segments {
			conservative = append(conservative, segment)
			if segment == "*" {
				conservative = append(conservative, "**")
			}
		}
		out.provenance = append(out.provenance, conservative)
	}
	return out
}

// ApplyPatterns masks paths using real tree segments: * matches one key,
// ** matches zero or more. Matched containers are masked as a whole.
func ApplyPatterns(tree map[string]any, patterns []string, redactor Redactor) map[string]any {
	if redactor == nil {
		redactor = DefaultRedactor
	}
	redactPatterns(tree, nil, compilePatterns(patterns).tree, redactor)
	return tree
}

// PatternsMatchValue checks historical values and their ancestors/descendants.
func PatternsMatchValue(value any, path string, patterns []string) bool {
	return compilePatterns(patterns).matchesValue(value, path)
}

func (p *Plan) PatternsMatchValue(value any, path string) bool {
	return p.patterns.matchesValue(value, path)
}

func (p patternPlan) matchesValue(value any, path string) bool {
	if len(p.provenance) == 0 {
		return false
	}
	segments := strings.Split(path, ".")
	for _, pattern := range p.provenance {
		for n := 1; n <= len(segments); n++ {
			if matchSegments(pattern, segments[:n]) {
				return true
			}
		}
	}
	hit := false
	redactPatterns(value, segments, p.provenance, func(_ string, v any) any { hit = true; return v })
	return hit
}

func redactPatterns(node any, path []string, patterns [][]string, redact Redactor) {
	if len(patterns) == 0 {
		return
	}
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
