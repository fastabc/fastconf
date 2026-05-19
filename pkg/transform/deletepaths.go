package transform

// DeletePaths returns a Transformer that removes the specified dotted-path
// keys from the tree. Missing paths are silently ignored.
func DeletePaths(paths ...string) Transformer {
	return TransformerFunc{
		NameStr: "DeletePaths",
		Fn: func(root map[string]any) error {
			for _, p := range paths {
				deletePath(root, p)
			}
			return nil
		},
	}
}
