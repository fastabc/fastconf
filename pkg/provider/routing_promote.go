package provider

import (
	"regexp"
	"strconv"
)

var indexedRoutingKeyPattern = regexp.MustCompile(`^(.+)\[(\d+)\]$`)

// PromoteIndexedRoutingKeys collapses sibling groups such as
// "domains[0]" / "domains[1]" into "domains": []any{...}. Groups that
// would collide with an existing base key are left untouched.
//
// Exported for callers that have already produced a tree (e.g. via a
// custom Provider) and want to apply only the indexed-key promotion.
// Internal RoutingLabelProvider callers go through transformRoutingTree
// which fuses promotion and leaf rewriting into a single tree walk.
func PromoteIndexedRoutingKeys(node map[string]any) {
	for _, value := range node {
		if child, ok := value.(map[string]any); ok {
			PromoteIndexedRoutingKeys(child)
		}
	}
	promoteIndexedKeysAtLayer(node)
}

// promoteIndexedKeysAtLayer promotes only at one map level. Callers must
// recurse separately (transformRoutingTree handles that fused with the
// leaf-rewrite pass).
func promoteIndexedKeysAtLayer(node map[string]any) {
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
		if err != nil {
			continue
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
}

// transformRoutingTree walks node bottom-up. Children are recursed
// first (so their leaves are typed and their indexed siblings are
// promoted), then this level's leaves are rewritten and its indexed
// siblings promoted. Single pass — no second walk to clean up.
func transformRoutingTree(node map[string]any, path []string, opts RoutingLabelOptions) {
	for k, v := range node {
		if child, ok := v.(map[string]any); ok {
			transformRoutingTree(child, append(append([]string(nil), path...), k), opts)
		}
	}
	rewriteLeavesAtLayer(node, path, opts)
	promoteIndexedKeysAtLayer(node)
}
