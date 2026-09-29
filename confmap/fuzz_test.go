package confmap

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

// FuzzDeep ensures Deep never panics and respects the type-conflict
// contract for arbitrary JSON-shaped inputs.
func FuzzDeep(f *testing.F) {
	f.Add([]byte(`{"a":1}`), []byte(`{"a":2}`))
	f.Add([]byte(`{"x":[1,2]}`), []byte(`{"x":[3]}`))
	f.Add([]byte(`{"m":{"k":1}}`), []byte(`{"m":{"k":2,"n":3}}`))
	f.Add([]byte(`{}`), []byte(`{"k":null}`))
	f.Add([]byte(`null`), []byte(`{"k":1}`))

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
	f.Add([]byte(`null`), []byte(`[{"op":"add","path":"/k","value":1}]`))

	f.Fuzz(func(t *testing.T, docRaw, patchRaw []byte) {
		var doc map[string]any
		if err := json.Unmarshal(docRaw, &doc); err != nil {
			t.Skip()
		}
		_, _ = ApplyPatch(doc, patchRaw)
	})
}

// FuzzPatchNumbers checks decimal normalization against exact rational arithmetic.
func FuzzPatchNumbers(f *testing.F) {
	f.Add("1.2300", "123e-2")
	f.Add("-0", "0e+100")
	f.Add("1"+strings.Repeat("0", 100), "1"+strings.Repeat("0", 99)+"1")
	f.Add("-0.001", "-1e-3")
	f.Fuzz(func(t *testing.T, a, b string) {
		for _, s := range []string{a, b} {
			// Bound the oracle's allocations, especially powers of ten.
			if len(s) == 0 || len(s) > 128 {
				t.Skip()
			}
			if _, err := json.Marshal(json.Number(s)); err != nil {
				t.Skip()
			}
			if i := strings.IndexAny(s, "eE"); i >= 0 {
				exp, err := strconv.Atoi(s[i+1:])
				if err != nil || exp < -1000 || exp > 1000 {
					t.Skip()
				}
			}
		}
		x, ok := new(big.Rat).SetString(a)
		if !ok {
			t.Fatal("oracle rejected valid JSON number:", a)
		}
		y, ok := new(big.Rat).SetString(b)
		if !ok {
			t.Fatal("oracle rejected valid JSON number:", b)
		}
		if got, want := patchEqual(json.Number(a), json.Number(b)), x.Cmp(y) == 0; got != want {
			t.Fatalf("%s == %s: got %v; want %v", a, b, got, want)
		}
	})
}
