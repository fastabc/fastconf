package codec_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/codec"
)

func TestJSON_Decode(t *testing.T) {
	decoder, err := codec.For("json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		want        map[string]any
		invalid     bool
	}{
		{name: "object", input: "{\"a\":1}  \n\t", want: map[string]any{"a": json.Number("1")}},
		{name: "large integer", input: `{"a":18446744073709551615}`, want: map[string]any{"a": json.Number("18446744073709551615")}},
		{name: "null", input: `null`, want: map[string]any{}},
		{name: "empty", want: map[string]any{}},
		{name: "second document", input: `{"a":1}{"a":2}`, invalid: true},
		{name: "trailing garbage", input: `{"a":1} trailing`, invalid: true},
		{name: "array root", input: `[]`, invalid: true},
		{name: "scalar root", input: `42`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decoder.Decode([]byte(tc.input))
			if tc.invalid {
				if err == nil {
					t.Fatalf("Decode=%v; want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode=%#v; want %#v", got, tc.want)
			}
		})
	}
}

func TestDecodeAny_JSON(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        any
		invalid     bool
	}{
		{name: "patch", input: `[{"op":"add","path":"/x","value":1}]`, want: []any{map[string]any{"op": "add", "path": "/x", "value": json.Number("1")}}},
		{name: "object", input: `{"a":1}`, want: map[string]any{"a": json.Number("1")}},
		{name: "empty", input: " \n\t"},
		{name: "second document", input: `[] []`, invalid: true},
		{name: "trailing garbage", input: `[] garbage`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codec.DecodeAny("json", []byte(tc.input))
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "json:") {
					t.Fatalf("DecodeAny=%v, %v; want json error", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DecodeAny=%#v; want %#v", got, tc.want)
			}
		})
	}
}
