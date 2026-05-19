package provider

import (
	"strings"

	"github.com/fastabc/fastconf/pkg/typed"
)

// rewriteLeavesAtLayer applies RoutingLabelOptions.Raw / KeepRawSuffixes /
// list-splitting / scalar coercion to every leaf at a single map level.
// It is called by transformRoutingTree after that level's children have
// already been rewritten and promoted, so it sees the fully-shaped
// neighbourhood.
func rewriteLeavesAtLayer(node map[string]any, path []string, opts RoutingLabelOptions) {
	for key, value := range node {
		s, ok := value.(string)
		if !ok {
			continue
		}
		nextPath := append(append([]string(nil), path...), key)
		node[key] = rewriteRoutingLeaf(s, strings.Join(nextPath, "."), opts)
	}
}

// rewriteRoutingLeaf turns one string leaf into either a typed scalar
// (bool/int/float) or a []any list (when ListSeparator splits it). Raw
// or KeepRaw paths short-circuit.
func rewriteRoutingLeaf(value, dottedPath string, opts RoutingLabelOptions) any {
	if opts.Raw {
		return value
	}
	if routingKeepRaw(dottedPath, opts.KeepRawSuffixes) {
		return value
	}
	separator := opts.ListSeparator
	if separator == "" {
		separator = ","
	}
	if !opts.NoListSplit && separator != "" && strings.Contains(value, separator) {
		rawParts := strings.Split(value, separator)
		parts := make([]any, 0, len(rawParts))
		for _, part := range rawParts {
			parts = append(parts, coerceRoutingScalar(strings.TrimSpace(part)))
		}
		return parts
	}
	return coerceRoutingScalar(value)
}

// routingKeepRaw returns true when dottedPath ends with a configured
// keep-raw suffix (defaulting to "rule" / "regexp"-like fields).
func routingKeepRaw(dottedPath string, configured []string) bool {
	suffixes := configured
	if suffixes == nil {
		suffixes = []string{".rule", "regexp"}
	}
	lowerPath := strings.ToLower(dottedPath)
	for _, suffix := range suffixes {
		if suffix == "" {
			continue
		}
		if strings.HasSuffix(lowerPath, strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

// coerceRoutingScalar applies the canonical bool→int→float→string
// ladder (whitespace trimmed, case insensitive) so a label "8080" becomes
// int64 and "TRUE " becomes true.
func coerceRoutingScalar(value string) any {
	return typed.Coerce(value, typed.CoerceOptions{TrimSpace: true, IgnoreCase: true})
}
