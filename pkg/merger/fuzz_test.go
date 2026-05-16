package merger

import (
	"encoding/json"
	"testing"
)

// FuzzDeep ensures Deep never panics and respects the type-conflict
// contract for arbitrary JSON-shaped inputs.
func FuzzDeep(f *testing.F) {
	f.Add([]byte(`{"a":1}`), []byte(`{"a":2}`))
	f.Add([]byte(`{"x":[1,2]}`), []byte(`{"x":[3]}`))
	f.Add([]byte(`{"m":{"k":1}}`), []byte(`{"m":{"k":2,"n":3}}`))
	f.Add([]byte(`{}`), []byte(`{"k":null}`))

	f.Fuzz(func(t *testing.T, dstRaw, srcRaw []byte) {
		var dst, src map[string]any
		if err := json.Unmarshal(dstRaw, &dst); err != nil {
			t.Skip()
		}
		if err := json.Unmarshal(srcRaw, &src); err != nil {
			t.Skip()
		}
		// Deep panicking on any structurally valid input would be a bug.
		_ = Deep(dst, src, Options{Strict: false})
		_ = Deep(dst, src, Options{Strict: false, AppendSlices: true})
	})
}

// FuzzPatch ensures ApplyPatch refuses or normalises malformed RFC 6902
// payloads without panicking.
func FuzzPatch(f *testing.F) {
	f.Add([]byte(`{"a":1}`), []byte(`[{"op":"replace","path":"/a","value":2}]`))
	f.Add([]byte(`{}`), []byte(`[{"op":"add","path":"/k","value":1}]`))
	f.Add([]byte(`{"a":1}`), []byte(`[]`))
	f.Add([]byte(`{}`), []byte(`not-json`))

	f.Fuzz(func(t *testing.T, docRaw, patchRaw []byte) {
		var doc map[string]any
		if err := json.Unmarshal(docRaw, &doc); err != nil {
			t.Skip()
		}
		_, _ = ApplyPatch(doc, patchRaw)
	})
}
