package transform

// SetIfAbsent sets a single dotted-path key only when it has no value.
func SetIfAbsent(path string, value any) Transformer {
	return TransformerFunc{
		NameStr: "SetIfAbsent(" + path + ")",
		Fn: func(root map[string]any) error {
			if _, ok := getPath(root, path); ok {
				return nil
			}
			setPath(root, path, value)
			return nil
		},
	}
}
