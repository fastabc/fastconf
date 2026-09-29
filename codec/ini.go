package codec

import (
	"fmt"

	"gopkg.in/ini.v1"
)

// iniDecoder keeps scalar values as strings for the target's typed hooks.
// Default-section keys live at the root; other sections are literal map keys.
type iniDecoder struct{}

func (iniDecoder) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	file, err := ini.LoadSources(ini.LoadOptions{
		IgnoreContinuation:        true,
		SpaceBeforeInlineComment:  true,
		UnescapeValueDoubleQuotes: true,
	}, data)
	if err != nil {
		return nil, fmt.Errorf("ini: %w", err)
	}
	out := map[string]any{}
	for _, section := range file.Sections() {
		target := out
		if name := section.Name(); name != ini.DefaultSection {
			if _, exists := out[name]; exists {
				return nil, fmt.Errorf("ini: section %q conflicts with a root key", name)
			}
			target = map[string]any{}
			out[name] = target
		}
		// Keys excludes inherited keys; Value avoids interpolation and coercion.
		for _, key := range section.Keys() {
			target[key.Name()] = key.Value()
		}
	}
	return out, nil
}
