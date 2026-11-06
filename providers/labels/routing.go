package labels

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/confmap"
)

// MaxRoutingIndex is the largest accepted list index in a routing label key such as
// domains[3].main. Promotion allocates index+1 slots, so a larger index fails Load instead of
// allocating an arbitrarily large (possibly overflowing) slice.
const MaxRoutingIndex = 1023

var indexedRoutingKeyPattern = regexp.MustCompile(`^(.+)\[(\d+)\]$`)

// collectRoutingLabelPairs normalises any of the accepted label inputs ([]string,
// map[string]string, ...) into a pair slice with each key optionally lowercased.
func collectRoutingLabelPairs(input any, lowercase bool) []confmap.LabelPair {
	pairs := confmap.NormalizeLabelInput(input)
	for i := range pairs {
		pairs[i].Key = normalizeRoutingKey(pairs[i].Key, lowercase)
	}
	return pairs
}

// normalizeRoutingKey applies the lowercase-keys option uniformly across the inspection and
// expansion paths.
func normalizeRoutingKey(key string, lowercase bool) string {
	if lowercase {
		return strings.ToLower(key)
	}
	return key
}

// routingGateBlocks skips labels only when the gate is present and not truthy.
// The last matching pair wins; an absent gate leaves the set enabled.
func routingGateBlocks(pairs []confmap.LabelPair, gate string) bool {
	if gate == "" {
		return false
	}
	var (
		value string
		found bool
	)
	for _, pair := range pairs {
		if pair.Key == gate {
			value = pair.Value
			found = true
		}
	}
	return found && !routingTruthy(value)
}

// routingTruthy mirrors Traefik / Docker label truthy semantics — case insensitive, surrounding
// whitespace tolerated.
func routingTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// transformRoutingTree walks node bottom-up. Children are recursed first (so their leaves are
// typed and their indexed siblings are promoted), then this level's leaves are rewritten and its
// indexed siblings promoted. Single pass — no second walk to clean up.
func transformRoutingTree(node map[string]any, path []string, opts *Routing) error {
	for k, v := range node {
		if child, ok := v.(map[string]any); ok {
			if err := transformRoutingTree(child, append(append([]string(nil), path...), k), opts); err != nil {
				return err
			}
		}
	}
	rewriteLeavesAtLevel(node, path, opts)
	return promoteIndexedKeysAtLevel(node, path)
}

// rewriteLeavesAtLevel applies Routing.Raw / KeepRawSuffixes / list-splitting / scalar coercion to
// every leaf at a single map level. It is called by transformRoutingTree after that level's
// children have already been rewritten and promoted, so it sees the fully-shaped neighbourhood.
func rewriteLeavesAtLevel(node map[string]any, path []string, opts *Routing) {
	for key, value := range node {
		s, ok := value.(string)
		if !ok {
			continue
		}
		nextPath := append(append([]string(nil), path...), key)
		node[key] = rewriteRoutingLeaf(s, strings.Join(nextPath, "."), opts)
	}
}

// rewriteRoutingLeaf turns one string leaf into either a typed scalar (bool/int/float) or a []any
// list (when ListSeparator splits it). Raw or KeepRaw paths short-circuit.
func rewriteRoutingLeaf(value, dottedPath string, opts *Routing) any {
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

// routingKeepRaw returns true when dottedPath ends with a configured keep-raw suffix (defaulting
// to "rule" / "regexp"-like fields).
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

// coerceRoutingScalar applies the canonical bool→int→float→string ladder (whitespace trimmed, case
// insensitive) so a label "8080" becomes int64 and "TRUE " becomes true.
func coerceRoutingScalar(value string) any {
	return confmap.Coerce(value, confmap.CoerceOptions{TrimSpace: true, IgnoreCase: true})
}

// promoteIndexedKeysAtLevel promotes only at one map level. Callers must recurse separately
// (transformRoutingTree handles that fused with the leaf-rewrite pass).
func promoteIndexedKeysAtLevel(node map[string]any, path []string) error {
	type indexedGroup struct {
		max    int
		values map[int]any
		keys   []string
	}
	groups := map[string]*indexedGroup{}
	for key, value := range node {
		matches := indexedRoutingKeyPattern.FindStringSubmatch(key)
		if len(matches) != 3 {
			continue
		}
		index, err := strconv.Atoi(matches[2])
		if err != nil || index > MaxRoutingIndex {
			return fmt.Errorf("labels: routing key %q: index exceeds %d", strings.Join(append(path, key), "."), MaxRoutingIndex)
		}
		base := matches[1]
		group := groups[base]
		if group == nil {
			group = &indexedGroup{max: -1, values: map[int]any{}}
			groups[base] = group
		}
		group.keys = append(group.keys, key)
		group.values[index] = value
		if index > group.max {
			group.max = index
		}
	}

	for base, group := range groups {
		if _, exists := node[base]; exists {
			continue
		}
		items := make([]any, group.max+1)
		for index, value := range group.values {
			items[index] = value
		}
		for _, key := range group.keys {
			delete(node, key)
		}
		node[base] = items
	}
	return nil
}
