package confmap

// DeepClone returns a fully-independent copy of a JSON-shaped tree.
// Maps and slices are duplicated recursively; scalar leaves are shared
// (immutable by JSON-shape convention). Nil input yields nil.
//
// The reload pipeline clones provider snapshots at the assembly
// boundary with this helper: Deep aliases source subtrees into the
// merged tree and later stages mutate that tree in place, so without a
// clone those writes would reach provider-owned maps, violating the
// ownership rule in contracts.Provider.Load.
//
// The handled type set (map[string]any, []any) MUST stay in lockstep
// with codec.normalize and the mutation walkers (Deep, secret, transform).
// Those all recurse the same JSON shape, so a typed container
// (map[any]any, []map[string]any) is opaque to every stage and never
// mutated — safe today. But if any walker is widened to recurse such a
// type, widen this clone too, or a provider subtree of that shape would
// escape the B1 ownership boundary.
func DeepClone(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return DeepClone(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return v
	}
}
