package providerutil

// CloneMap returns a shallow copy of m so providers can return mutable
// snapshots without exposing their internal cache.
func CloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
