package labels

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var indexedRoutingKeyPattern = regexp.MustCompile(`^(.+)\[(\d+)\]$`)

// MaxRoutingIndex is the largest accepted list index in a routing label key such as
// domains[3].main. Promotion allocates index+1 slots, so a larger index fails Load instead of
// allocating an arbitrarily large (possibly overflowing) slice.
const MaxRoutingIndex = 1023

// promoteIndexedKeysAtLayer promotes only at one map level. Callers must recurse separately
// (transformRoutingTree handles that fused with the leaf-rewrite pass).
func promoteIndexedKeysAtLayer(node map[string]any, path []string) error {
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
	rewriteLeavesAtLayer(node, path, opts)
	return promoteIndexedKeysAtLayer(node, path)
}
