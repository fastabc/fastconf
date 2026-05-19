package mappath

import "strings"

// LabelPair is an ordered key/value pair as produced by NormalizeLabelInput.
// Order matters: callers that need gate-style "first matching key wins"
// semantics rely on the input order being preserved.
type LabelPair struct {
	Key   string
	Value string
}

// NormalizeLabelInput converts the common label-input shapes into an
// ordered slice of (key, value) pairs:
//
//   - []string{"key=value", ...}   matching the Compose / docker CLI form
//   - []any{"key=value", ...}      matching YAML-decoded slice form
//   - map[string]string            matching the Docker engine / K8s form
//   - map[string]any (values must be strings)
//
// Inputs that are not one of these shapes return nil. Entries that lack an
// '=' separator are silently dropped. Order is preserved for slice inputs;
// map inputs follow Go's randomized iteration order.
func NormalizeLabelInput(input any) []LabelPair {
	switch x := input.(type) {
	case []string:
		out := make([]LabelPair, 0, len(x))
		for _, kv := range x {
			if k, v, ok := strings.Cut(kv, "="); ok {
				out = append(out, LabelPair{Key: k, Value: v})
			}
		}
		return out
	case []any:
		out := make([]LabelPair, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if k, v, ok := strings.Cut(s, "="); ok {
				out = append(out, LabelPair{Key: k, Value: v})
			}
		}
		return out
	case map[string]string:
		out := make([]LabelPair, 0, len(x))
		for k, v := range x {
			out = append(out, LabelPair{Key: k, Value: v})
		}
		return out
	case map[string]any:
		out := make([]LabelPair, 0, len(x))
		for k, v := range x {
			s, ok := v.(string)
			if !ok {
				continue
			}
			out = append(out, LabelPair{Key: k, Value: s})
		}
		return out
	}
	return nil
}
