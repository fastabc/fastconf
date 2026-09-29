package confmap

import "strings"

// LabelOptions controls how ExpandLabels reshapes flat labels into a nested map[string]any.
type LabelOptions struct {
	// Prefix, when non-empty, restricts expansion to labels whose key starts
	// with this prefix (e.g. "routing."). Labels lacking the prefix are
	// silently skipped.
	Prefix string
	// StripPrefix removes Prefix from each key before expansion. Has no
	// effect when Prefix is empty.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".". Use
	// Separators (plural) when more than one delimiter is in play (e.g.
	// K8s recommended labels with both "/" and "."). When both fields are
	// set, Separators wins.
	Separator string
	// Separators is the ordered list of delimiters applied to each key.
	// Splits happen left-to-right: the input is first split by Separators[0],
	// then each segment is split by Separators[1], and so on. This lets
	// K8s-style "app.kubernetes.io/name" decompose coherently — e.g.
	// {"/", "."} produces parts ["app", "kubernetes", "io", "name"].
	// When empty, Separator (singular) is used; when both are empty, "."
	// is the fallback.
	Separators []string
	// Coerce, when true, converts "true" / "false" / int-like / float-like
	// values into their typed forms (matching provider env coercion).
	// Default false: values are kept verbatim as strings.
	Coerce bool
}

// ExpandLabels reshapes a flat list / map of "dotted.key=value" labels into a
// nested map[string]any. Accepted input shapes:
//
//   - []string{"a.b=1", "a.c=2"}            — Compose / docker CLI form
//   - []any{"a.b=1", "a.c=2"}               — YAML-decoded form
//   - map[string]string{"a.b":"1","a.c":"2"}— Docker engine / K8s annotation form
//   - map[string]any{"a.b":"1","a.c":"2"}   — already-decoded YAML map
//
// Malformed entries (no '=' separator, empty key after prefix trim) are
// silently dropped. The result is a freshly allocated tree; callers may merge
// it into an existing root via Deep.
func ExpandLabels(input any, opts LabelOptions) map[string]any {
	seps := resolveSeparators(opts)
	out := map[string]any{}
	for _, pair := range NormalizeLabelInput(input) {
		k := pair.Key
		if opts.Prefix != "" {
			if !strings.HasPrefix(k, opts.Prefix) {
				continue
			}
			if opts.StripPrefix {
				k = strings.TrimPrefix(k, opts.Prefix)
				// Strip a leading delimiter introduced by the prefix
				// boundary, regardless of which separator it is.
				for _, s := range seps {
					if s != "" && strings.HasPrefix(k, s) {
						k = strings.TrimPrefix(k, s)
						break
					}
				}
			}
		}
		if k == "" {
			continue
		}
		parts := splitMulti(k, seps)
		var value any = pair.Value
		if opts.Coerce {
			value = coerceLabelValue(pair.Value)
		}
		Set(out, parts, value)
	}
	return out
}

// resolveSeparators returns the effective separator list, honoring Separators when non-empty, then
// Separator (singular), then the fallback ".".
func resolveSeparators(opts LabelOptions) []string {
	if len(opts.Separators) > 0 {
		seps := make([]string, 0, len(opts.Separators))
		for _, s := range opts.Separators {
			if s != "" {
				seps = append(seps, s)
			}
		}
		if len(seps) > 0 {
			return seps
		}
	}
	if opts.Separator != "" {
		return []string{opts.Separator}
	}
	return []string{"."}
}

// splitMulti splits s by seps[0], then each resulting segment by seps[1], and so on. Empty
// segments are dropped so back-to-back delimiters do not create empty path components.
func splitMulti(s string, seps []string) []string {
	parts := []string{s}
	for _, sep := range seps {
		next := make([]string, 0, len(parts))
		for _, p := range parts {
			for _, sub := range strings.Split(p, sep) {
				if sub != "" {
					next = append(next, sub)
				}
			}
		}
		parts = next
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

// coerceLabelValue mirrors the provider env coercion: bool / int64 / float64 / string in that
// order. Case-sensitive and whitespace-preserving — labels that have already been canonicalized by
// ExpandLabels do not need either rung. Use Coerce directly for callers that need a different
// policy.
func coerceLabelValue(s string) any {
	return Coerce(s, CoerceOptions{})
}

// LabelPair is an ordered key/value pair as produced by NormalizeLabelInput. Order matters:
// callers that need gate-style "first matching key wins" semantics rely on the input order being
// preserved.
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
