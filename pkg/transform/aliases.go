package transform

// Aliases returns a Transformer that rewrites legacy keys to their new
// home. If the target path already has a value the new world wins and
// the alias is dropped.
func Aliases(mapping map[string]string) Transformer {
	return TransformerFunc{
		NameStr: "Aliases",
		Fn: func(root map[string]any) error {
			for from, to := range mapping {
				v, ok := getPath(root, from)
				if !ok {
					continue
				}
				if _, exists := getPath(root, to); !exists {
					setPath(root, to, v)
				}
				deletePath(root, from)
			}
			return nil
		},
	}
}
