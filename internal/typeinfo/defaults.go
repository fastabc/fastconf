package typeinfo

import "strings"

// ErrRemovedFCDefault explains the replacement for the zero-value default token
// removed from the fc tag in v1.7.0.
const ErrRemovedFCDefault = `fc:"default=…" was removed in v1.7.0; use default:"…" for absent keys, or a Defaults() method for computed and zero-value defaults`

// HasRemovedFCDefault reports whether an fc tag still declares the removed
// default= token, so callers can reject it instead of silently ignoring it.
func HasRemovedFCDefault(tag string) bool {
	if tag == "" {
		return false
	}
	for token := range strings.SplitSeq(tag, ",") {
		if strings.HasPrefix(strings.TrimSpace(token), "default=") {
			return true
		}
	}
	return false
}
