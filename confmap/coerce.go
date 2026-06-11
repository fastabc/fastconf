// Scalar-value helpers shared by providers and map-path expansion.
package confmap

import (
	"strconv"
	"strings"
)

// CoerceOptions tunes how Coerce interprets a string.
type CoerceOptions struct {
	// TrimSpace strips surrounding whitespace before matching boolean
	// literals and parsing numbers. The trimmed value is also what gets
	// returned when no typed parse succeeds — keeping the ladder
	// deterministic regardless of incidental whitespace.
	TrimSpace bool
	// IgnoreCase lowercases the candidate before matching the boolean
	// literals "true" / "false". Numeric parsing is unaffected — Go's
	// strconv already accepts upper or lower case for hex / exponent
	// formats.
	IgnoreCase bool
}

// Coerce converts s into a typed Go value using the canonical
// bool → int64 → float64 → string ladder. Behavior is deterministic
// across env, routing-labels, and label expansion: tune the rung with
// CoerceOptions rather than forking the ladder per call site.
func Coerce(s string, opts CoerceOptions) any {
	if opts.TrimSpace {
		s = strings.TrimSpace(s)
	}
	candidate := s
	if opts.IgnoreCase {
		candidate = strings.ToLower(s)
	}
	switch candidate {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
