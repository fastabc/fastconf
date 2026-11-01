package confmap

// DeepClone independently copies map[string]any and []any containers, preserving nil;
// scalar leaves are shared. Typed containers are opaque. Its supported shapes must
// stay aligned with every mutation walker, or provider-owned data could be mutated.
func DeepClone(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = CloneValue(v)
	}
	return out
}

// CloneValue deep-copies one JSON-shaped value with the same rules as DeepClone: map[string]any
// and []any are duplicated recursively, every other value is returned as-is.
func CloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return DeepClone(t)
	case []any:
		if t == nil {
			return t
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = CloneValue(e)
		}
		return out
	default:
		return v
	}
}
