package confmap_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fastabc/fastconf/confmap"
)

func TestDeep(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dst, src  map[string]any
		options   confmap.Options
		want      map[string]any
		errorPath string
	}{
		{name: "empty into nil"},
		{name: "nonempty into nil", src: map[string]any{"port": 80}, errorPath: "nil destination"},
		{name: "add key", dst: map[string]any{"a": 1}, src: map[string]any{"b": 2}, want: map[string]any{"a": 1, "b": 2}},
		{name: "overwrite scalar", dst: map[string]any{"a": 1}, src: map[string]any{"a": 2}, want: map[string]any{"a": 2}},
		{name: "nested merge", dst: map[string]any{"db": map[string]any{"host": "local", "port": 1}}, src: map[string]any{"db": map[string]any{"port": 2}}, want: map[string]any{"db": map[string]any{"host": "local", "port": 2}}},
		{name: "nested nil destination", dst: map[string]any{"db": map[string]any(nil)}, src: map[string]any{"db": map[string]any{"port": 80}}, want: map[string]any{"db": map[string]any{"port": 80}}},
		{name: "replace slice", dst: map[string]any{"items": []any{1, 2}}, src: map[string]any{"items": []any{3}}, want: map[string]any{"items": []any{3}}},
		{name: "append slice", dst: map[string]any{"items": []any{1, 2}}, src: map[string]any{"items": []any{3}}, options: confmap.Options{AppendSlices: true}, want: map[string]any{"items": []any{1, 2, 3}}},
		{name: "lenient map to scalar", dst: map[string]any{"a": map[string]any{"b": 1}}, src: map[string]any{"a": "leaf"}, want: map[string]any{"a": "leaf"}},
		{name: "strict map to scalar", dst: map[string]any{"a": map[string]any{"b": 1}}, src: map[string]any{"a": "leaf"}, options: confmap.Options{Strict: true}, errorPath: "a"},
		{name: "strict slice to scalar", dst: map[string]any{"a": []any{1}}, src: map[string]any{"a": 1}, options: confmap.Options{Strict: true}, errorPath: "a"},
		{name: "strict compatible numbers", dst: map[string]any{"a": 1}, src: map[string]any{"a": json.Number("2.5")}, options: confmap.Options{Strict: true}, want: map[string]any{"a": json.Number("2.5")}},
		{name: "strict stringer is not numeric", dst: map[string]any{"at": time.Unix(0, 0)}, src: map[string]any{"at": 42}, options: confmap.Options{Strict: true}, errorPath: "at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := confmap.DeepClone(tc.src)
			err := confmap.Deep(tc.dst, tc.src, tc.options)
			if tc.errorPath != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorPath) {
					t.Fatalf("Deep = %v; want error naming %q", err, tc.errorPath)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(tc.dst, tc.want) {
					t.Fatalf("merged = %#v; want %#v", tc.dst, tc.want)
				}
			}
			if !reflect.DeepEqual(tc.src, original) {
				t.Fatal("merge mutated the source")
			}
		})
	}
}
