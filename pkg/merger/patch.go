package merger

import (
	"encoding/json"
	"fmt"

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
