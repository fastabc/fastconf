package confmap

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// FuzzDeep checks lenient merge, source ownership and replacement idempotence.
func FuzzDeep(f *testing.F) {
	for _, seed := range [][2]string{{`{"a":1}`, `{"a":2}`}, {`{"x":[1,2]}`, `{"x":[3]}`}, {`{"m":{"k":1}}`, `{"m":{"k":2,"n":3}}`}, {`{}`, `{"k":null}`}, {`null`, `{"k":1}`}} {
		f.Add([]byte(seed[0]), []byte(seed[1]))
	}
	f.Fuzz(func(t *testing.T, dstRaw, srcRaw []byte) {
		var dst, src map[string]any
		if json.Unmarshal(dstRaw, &dst) != nil || json.Unmarshal(srcRaw, &src) != nil {
			t.Skip()
		}
		for _, appendSlices := range []bool{false, true} {
			target, source := DeepClone(dst), DeepClone(src)
			err := Deep(target, source, Options{AppendSlices: appendSlices})
			if dst == nil && len(src) > 0 {
				if err == nil {
					t.Fatal("merge into nil destination succeeded")
				}
				continue
			}
			if err != nil {
				t.Fatalf("lenient merge: %v", err)
			}
			if !reflect.DeepEqual(source, src) {
				t.Fatal("merge changed its source")
			}
			if !appendSlices {
				before := DeepClone(target)
				if err := Deep(target, source, Options{}); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(target, before) {
					t.Fatal("replacement merge is not idempotent")
				}
			}
		}
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
		before := DeepClone(doc)
		_, _ = ApplyPatch(doc, patchRaw)
		if !reflect.DeepEqual(doc, before) {
			t.Fatal("patch mutated its input document")
		}
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
