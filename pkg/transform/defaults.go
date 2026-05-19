package transform

// Defaults returns a Transformer that recursively merges the supplied
// values into the tree, only filling keys that are missing.
func Defaults(values map[string]any) Transformer {
	return TransformerFunc{
		NameStr: "Defaults",
		Fn: func(root map[string]any) error {
			mergeDefaults(root, values)
			return nil
		},
	}
}

func mergeDefaults(dst, src map[string]any) {
	for k, sv := range src {
		dv, exists := dst[k]
		if !exists {
			dst[k] = cloneAny(sv)
			continue
		}
		dm, dok := dv.(map[string]any)
		sm, sok := sv.(map[string]any)
		if dok && sok {
			mergeDefaults(dm, sm)
		}
	}
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = cloneAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = cloneAny(vv)
		}
		return out
	default:
		return v
	}
}
