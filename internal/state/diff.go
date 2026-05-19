package state

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ValueMap turns any pointer into a generic map[string]any view by
// round-tripping through encoding/json. Used by State[T].Diff and the
// secret redactor: both want the user's struct tags to govern field
// names, which only json.Marshal supplies.
//
// Returns nil for a nil input, an unmarshalable value, or an empty
// encoding ("null" / "{}"). Safe for any concrete type — callers in
// root pass *T directly.
func ValueMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	_ = json.Unmarshal(buf, &out)
	return out
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

// DiffMaps returns sorted dotted-path differences between two generic
// maps. Nested maps recurse with a dotted prefix; scalar equality uses
// json-canonical comparison so map ordering does not produce spurious
// diffs.
func DiffMaps(prefix string, a, b map[string]any) []DiffEntry {
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
				out = append(out, DiffMaps(full, ma, mb)...)
				continue
			}
			ja, _ := json.Marshal(va)
			jb, _ := json.Marshal(vb)
			if string(ja) != string(jb) {
				out = append(out, DiffEntry{Path: full, Change: DiffModified, Before: va, After: vb})
			}
		}
	}
	return out
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
