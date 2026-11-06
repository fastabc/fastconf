// Package secret implements the redaction and resolver primitives behind
// fastconf's `fc:"secret"` tag and WithSecretResolver hook.
package secret

import (
	"strings"
)

// Redactor turns a sensitive value into its display form. It receives
// the dotted path and the raw decoded value, and returns whatever should
// be surfaced in dumps, logs and CLI output.
type Redactor func(path string, value any) any

// DefaultRedactor replaces the value with "***REDACTED***".
func DefaultRedactor(_ string, _ any) any { return "***REDACTED***" }

// HasTag reports whether tag contains the bare "secret" token in a
// comma-separated `fc` struct tag.
func HasTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, p := range strings.Split(tag, ",") {
		if strings.TrimSpace(p) == "secret" {
			return true
		}
	}
	return false
}
