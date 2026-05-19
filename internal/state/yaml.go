package state

import (
	"sort"

	"gopkg.in/yaml.v3"
)

// UnflattenForYAML turns a flat dotted-key map (AllSettings shape) into
// a nested map[string]any tree. The State.Dump method uses this to
// convert Introspect().Settings() into a tree shape suitable for
// yaml.Marshal.
func UnflattenForYAML(flat map[string]any) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		setDottedYAML(out, k, flat[k])
	}
	return out
}

func setDottedYAML(root map[string]any, dotted string, v any) {
	cur := root
	start := 0
	for i := 0; i <= len(dotted); i++ {
		if i == len(dotted) || dotted[i] == '.' {
			part := dotted[start:i]
			if i == len(dotted) {
				cur[part] = v
				return
			}
			next, ok := cur[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[part] = next
			}
			cur = next
			start = i + 1
		}
	}
}

// orderedYAMLNode recursively converts map[string]any into a yaml.Node
// with keys sorted lexicographically. yaml.v3 does not preserve map
// order otherwise, so operator-driven diff would flake.
func orderedYAMLNode(v any) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		node := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				orderedYAMLNode(t[k]),
			)
		}
		return node
	case []any:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range t {
			node.Content = append(node.Content, orderedYAMLNode(e))
		}
		return node
	default:
		n := &yaml.Node{}
		_ = n.Encode(v)
		return n
	}
}
