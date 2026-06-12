package confmap

import (
	"encoding/json"
	"fmt"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

// ApplyPatch applies an RFC 6902 JSON Patch (provided as a JSON-encoded array)
// to the merged document. The document is round-tripped through JSON to take
// advantage of the upstream library; this is acceptable because patching only
// happens during reload, never on the hot Get() path.
//
// The returned map replaces the caller's document on success. On failure, the
// caller MUST keep its previous document untouched (failure-safe pipeline).
func ApplyPatch(doc map[string]any, patchJSON []byte) (map[string]any, error) {
	if len(patchJSON) == 0 {
		return doc, nil
	}
	patch, err := jsonpatch.DecodePatch(patchJSON)
	if err != nil {
		return nil, fmt.Errorf("merger: decode patch: %w", err)
	}
	docJSON, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("merger: marshal doc: %w", err)
	}
	out, err := patch.Apply(docJSON)
	if err != nil {
		return nil, fmt.Errorf("merger: apply patch: %w", err)
	}
	var next map[string]any
	if err := json.Unmarshal(out, &next); err != nil {
		return nil, fmt.Errorf("merger: unmarshal patched: %w", err)
	}
	return next, nil
}

// PatchPaths returns the dotted destination paths named by an RFC 6902
// ops array. Only "path" is reported: for move/copy the value lands at
// "path", so that is the field a patch actually contributes. JSON Pointer
// escapes (~1 → "/", ~0 → "~") are decoded. A path is truncated at the
// first numeric array-index segment because the provenance index records
// a whole []any under its parent map path (recordTreeDepth's slice
// branch) — the slice's parent is the real attributable leaf, and
// anything past the index would be a phantom key. Empty / root paths and
// a leading-index path yield "".
func PatchPaths(patchJSON []byte) ([]string, error) {
	if len(patchJSON) == 0 {
		return nil, nil
	}
	var ops []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(patchJSON, &ops); err != nil {
		return nil, fmt.Errorf("merger: decode patch paths: %w", err)
	}
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, pointerToDotted(op.Path))
	}
	return out, nil
}

// pointerToDotted converts an RFC 6901 JSON Pointer to the dotted path
// grammar used by the provenance index.
func pointerToDotted(ptr string) string {
	if ptr == "" || ptr == "/" {
		return ""
	}
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		// RFC 6901: decode ~1 before ~0.
		s = strings.ReplaceAll(s, "~1", "/")
		s = strings.ReplaceAll(s, "~0", "~")
		if isAllDigits(s) {
			// Array index: provenance records the whole slice under its
			// parent map path, so truncate — the parent is the real leaf.
			break
		}
		out = append(out, s)
	}
	return strings.Join(out, ".")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PatchBytesFromAny converts a decoded patch payload (either []any or
// already-encoded raw JSON) to the JSON byte form expected by ApplyPatch.
func PatchBytesFromAny(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	return json.Marshal(v)
}
