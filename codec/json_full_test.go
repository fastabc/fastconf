package codec_test

import (
	"strings"
	"testing"

	"github.com/fastabc/fastconf/codec"
)

func TestJSONCodecConsumesWholeDocument(t *testing.T) {
	p, _ := codec.Lookup("json")
	for _, tc := range []struct {
		name, input string
		wantErr     bool
	}{
		{"object", "{\"a\":1}   \n\t", false},
		{"second", `{"a":1}{"a":2}`, true},
		{"garbage", `{"a":1} trailing`, true},
		{"empty", ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Decode([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDecodeAnyConsumesWholeJSONPatch(t *testing.T) {
	for _, input := range []string{`[{"op":"add","path":"/x","value":1}]`, "  \n\t"} {
		if _, err := codec.DecodeAny("json", []byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []string{`[] []`, `[] garbage`} {
		if _, err := codec.DecodeAny("json", []byte(input)); err == nil || !strings.Contains(err.Error(), "json:") {
			t.Fatalf("input %q err=%v", input, err)
		}
	}
}
