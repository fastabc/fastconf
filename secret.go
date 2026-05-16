package fastconf

// Secret tagging and redaction.
//
// Mark struct fields as sensitive with `fastconf:"secret"` (or include the
// `secret` token in a comma-separated tag list). FastConf will collect their
// dotted JSON paths during type registration and use the configured
// SecretRedactor to mask their values when callers ask for a redacted view.
//
// The default redactor replaces the value with a fixed sentinel string.

import (
	"reflect"
	"sort"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// SecretRedactor turns a sensitive value into its display form. It receives
// the dotted path and the raw decoded value, and returns whatever should be
// surfaced in dumps, logs and CLI output.
type SecretRedactor func(path string, value any) any

// DefaultSecretRedactor replaces the value with "***REDACTED***".
func DefaultSecretRedactor(_ string, _ any) any { return "***REDACTED***" }

var secretCache = typeinfo.NewCache[[]string]()

// secretPathsFor returns the cached, sorted list of dotted paths whose
// fields carry the `fastconf:"secret"` marker. The walk understands json/yaml
// tags for naming and recurses into anonymous embeds, named struct fields and
// pointer-to-struct.
func secretPathsFor(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return secretCache.GetOrCompute(t, func() []string {
		seen := map[string]struct{}{}
		typeinfo.Walk(t, typeinfo.WalkFunc(func(path string, _ []int, f reflect.StructField, _ *reflect.Type) bool {
			if hasSecretTag(f.Tag.Get("fastconf")) {
				seen[path] = struct{}{}
			}
			return true
		}))
		out := make([]string, 0, len(seen))
		for p := range seen {
			out = append(out, p)
		}
		sort.Strings(out)
		return out
	})
}

func hasSecretTag(tag string) bool {
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

// Redact returns a deep copy of v with every secret path replaced according
// to the redactor (DefaultSecretRedactor when nil).
func (s *State[T]) Redact(redactor SecretRedactor) map[string]any {
	if s == nil || s.Value == nil {
		return nil
	}
	if redactor == nil {
		redactor = DefaultSecretRedactor
	}
	paths := secretPathsFor(reflect.TypeOf(*s.Value))
	m := stateValueMap(s)
	for _, p := range paths {
		applyRedaction(m, strings.Split(p, "."), p, redactor)
	}
	return m
}

func applyRedaction(m map[string]any, parts []string, path string, redactor SecretRedactor) {
	if len(parts) == 0 || m == nil {
		return
	}
	head := parts[0]
	if len(parts) == 1 {
		if v, ok := m[head]; ok {
			m[head] = redactor(path, v)
		}
		return
	}
	next := parts[1]
	switch {
	case next == "[]":
		// Iterate every slice element and continue with the remaining
		// path on each element. Skip when the value isn't a slice.
		arr, ok := m[head].([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			if child, ok := item.(map[string]any); ok {
				applyRedaction(child, parts[2:], path, redactor)
			}
		}
	case next == "{}":
		// Iterate every map value and continue with the remaining path.
		mm, ok := m[head].(map[string]any)
		if !ok {
			return
		}
		for _, v := range mm {
			if child, ok := v.(map[string]any); ok {
				applyRedaction(child, parts[2:], path, redactor)
			}
		}
	default:
		child, ok := m[head].(map[string]any)
		if !ok {
			return
		}
		applyRedaction(child, parts[1:], path, redactor)
	}
}
