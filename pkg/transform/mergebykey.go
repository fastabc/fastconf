package transform

import "fmt"

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
