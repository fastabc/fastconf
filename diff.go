package fastconf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/fastabc/fastconf/confmap"
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

type diffEntry struct {
	DiffEntry
	segs []string
}

// diffMaps returns sorted dotted-path differences between two generic
// maps, retaining path segments for unambiguous value lookup. Scalar
// equality uses JSON comparison so map ordering does not produce diffs.
func diffMaps(prefix []string, a, b map[string]any) []diffEntry {
	path := make([]string, len(prefix), len(prefix)+16)
	copy(path, prefix)
	return appendDiffs(nil, path, a, b)
}

func appendDiffs(out []diffEntry, prefix []string, a, b map[string]any) []diffEntry {
	keys := make(map[string]struct{}, len(a)+len(b))
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
	for _, k := range ordered {
		path := append(prefix, k)
		va, oka := a[k]
		vb, okb := b[k]
		change := DiffModified
		switch {
		case oka && !okb:
			change = DiffRemoved
		case !oka && okb:
			change = DiffAdded
		default:
			ma, _ := va.(map[string]any)
			mb, _ := vb.(map[string]any)
			if ma != nil && mb != nil {
				out = appendDiffs(out, path, ma, mb)
				continue
			}
			if jsonEqual(va, vb) {
				continue
			}
		}
		out = append(out, diffEntry{DiffEntry: DiffEntry{Path: strings.Join(path, "."), Change: change, Before: va, After: vb}, segs: slices.Clone(path)})
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
	entries := diffMaps(nil, a, b)
	if len(entries) == 0 {
		return nil
	}
	a, b = before.Map(), after.Map()
	out := make([]DiffEntry, len(entries))
	for i, e := range entries {
		out[i] = e.DiffEntry
		out[i].Before, _ = confmap.Get(a, e.segs...)
		out[i].After, _ = confmap.Get(b, e.segs...)
	}
	return out
}
