package fastconf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

// valueMapChecked turns any pointer into a generic map[string]any view by
// round-tripping through encoding/json, so the user's struct tags govern
// field names. It preserves JSON numbers and reports serialization
// failures; a nil input yields a nil map.
func valueMapChecked(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// DiffChange classifies one DiffEntry as an add, removal, or in-place
// modification of a dotted path between two State values.
type DiffChange uint8

const (
	// DiffAdded means the path exists in the right-hand map but not the left.
	DiffAdded DiffChange = iota
	// DiffRemoved means the path exists in the left-hand map but not the right.
	DiffRemoved
	// DiffModified means the path exists in both with non-equal scalar values.
	DiffModified
)

func (c DiffChange) String() string {
	switch c {
	case DiffAdded:
		return "added"
	case DiffRemoved:
		return "removed"
	case DiffModified:
		return "modified"
	default:
		return fmt.Sprintf("DiffChange(%d)", uint8(c))
	}
}

// DiffEntry is a single structured difference between two State snapshots.
// Consumers (PR-bots, audit sinks, fastconfctl plan) can filter or sort
// by Change / Path without parsing a rendered string.
//
//   - Before is nil for DiffAdded.
//   - After  is nil for DiffRemoved.
//   - Both are populated for DiffModified.
type DiffEntry struct {
	Path   string
	Change DiffChange
	Before any
	After  any
}

// diffMaps returns sorted dotted-path differences between two generic
// maps. Nested maps recurse with a dotted prefix; scalar equality uses
// json-canonical comparison so map ordering does not produce spurious
// diffs.
func diffMaps(prefix string, a, b map[string]any) []DiffEntry {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var out []DiffEntry
	for _, k := range ordered {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		va, oka := a[k]
		vb, okb := b[k]
		switch {
		case oka && !okb:
			out = append(out, DiffEntry{Path: full, Change: DiffRemoved, Before: va})
		case !oka && okb:
			out = append(out, DiffEntry{Path: full, Change: DiffAdded, After: vb})
		default:
			ma, _ := va.(map[string]any)
			mb, _ := vb.(map[string]any)
			if ma != nil && mb != nil {
				out = append(out, diffMaps(full, ma, mb)...)
				continue
			}
			if !jsonEqual(va, vb) {
				out = append(out, DiffEntry{Path: full, Change: DiffModified, Before: va, After: vb})
			}
		}
	}
	return out
}

// jsonEqual avoids marshaling ordinary scalars. Mixed types and unusual
// encodings retain the original JSON comparison, including marshal failures.
func jsonEqual(a, b any) bool {
	switch a := a.(type) {
	case nil, bool, json.Number:
		if a == b {
			return true
		}
	case string:
		if b, ok := b.(string); ok && utf8.ValidString(a) && utf8.ValidString(b) {
			return a == b
		}
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// FormatDiff renders a DiffEntry sequence as the human-readable line
// list that earlier FastConf versions returned from Diff. Rendering is
// not part of any SemVer contract — callers that need stable machine
// output should consume DiffEntry fields directly.
func FormatDiff(entries []DiffEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		switch e.Change {
		case DiffAdded:
			out[i] = fmt.Sprintf("+ %s = %v", e.Path, e.After)
		case DiffRemoved:
			out[i] = fmt.Sprintf("- %s = %v", e.Path, e.Before)
		case DiffModified:
			out[i] = fmt.Sprintf("~ %s : %v -> %v", e.Path, e.Before, e.After)
		default:
			out[i] = fmt.Sprintf("? %s", e.Path)
		}
	}
	return out
}

// diagnosticDiff detects changes before redaction, then substitutes safe views.
// Secret-only changes remain visible even when both displayed values are masked.
func diagnosticDiff[T any](before, after *State[T]) []DiffEntry {
	a, _ := before.rawTreeChecked()
	b, _ := after.rawTreeChecked()
	entries := diffMaps("", a, b)
	a, b = diagnosticValues(before.Map()), diagnosticValues(after.Map())
	for i := range entries {
		entries[i].Before = a[entries[i].Path]
		entries[i].After = b[entries[i].Path]
	}
	return entries
}

func diagnosticValues(tree map[string]any) map[string]any {
	out := make(map[string]any)
	var walk func(map[string]any, string)
	walk = func(m map[string]any, prefix string) {
		for k, v := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out[path] = v
			if child, ok := v.(map[string]any); ok {
				walk(child, path)
			}
		}
	}
	walk(tree, "")
	return out
}
