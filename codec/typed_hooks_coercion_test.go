package codec_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fastabc/fastconf/codec"
)

type textLevel int

func (*textLevel) UnmarshalText([]byte) error { return nil }

func TestListCoercion_CommaAndJSONStrings(t *testing.T) {
	type C struct {
		Hosts []string `json:"hosts"`
		Ports []int    `json:"ports"`
		Raw   []byte   `json:"raw"`
		Bytes [2]byte  `json:"bytes"`
	}
	p := codec.BuildTypedHookPlan(reflect.TypeFor[C](), codec.DefaultTypedHooks(), codec.WithListCoercion())
	m := map[string]any{"hosts": "a, b", "ports": `[80, "443"]`, "raw": "aGk=", "bytes": "a,b"}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"hosts": []any{"a", "b"}, "ports": []any{json.Number("80"), int64(443)}, "raw": "aGk=", "bytes": "a,b"}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %#v, want %#v", m, want)
	}
}
func TestListCoercion_IndexKeyedMap(t *testing.T) {
	type C struct {
		V []string `json:"v"`
	}
	for _, tc := range []struct{ in, want any }{
		{map[string]any{"0": "a", "1": "b"}, []any{"a", "b"}},
		{map[string]any{"0": "a", "2": "b"}, map[string]any{"0": "a", "2": "b"}},
		{map[string]any{"00": "a"}, map[string]any{"00": "a"}},
		{"", []any{}},
	} {
		m := map[string]any{"v": tc.in}
		p := codec.BuildTypedHookPlan(reflect.TypeFor[C](), nil, codec.WithListCoercion())
		if err := p.Apply(m); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(m["v"], tc.want) {
			t.Fatalf("got %#v, want %#v", m["v"], tc.want)
		}
	}
}
func TestListCoercion_InvalidJSONArray(t *testing.T) {
	type C struct {
		V []int `json:"v"`
	}
	p := codec.BuildTypedHookPlan(reflect.TypeFor[C](), codec.DefaultTypedHooks(), codec.WithListCoercion())
	for _, s := range []string{"[1,", "[1] false"} {
		if err := p.Apply(map[string]any{"v": s}); err == nil || !strings.HasPrefix(err.Error(), "list coercion:") {
			t.Fatalf("%q: %v", s, err)
		}
	}
}
func TestListCoercion_OffWithoutOption(t *testing.T) {
	type C struct {
		V []string `json:"v"`
	}
	m := map[string]any{"v": "a,b"}
	if err := codec.BuildTypedHookPlan(reflect.TypeFor[C](), codec.DefaultTypedHooks()).Apply(m); err != nil || m["v"] != "a,b" {
		t.Fatalf("%v %v", m, err)
	}
}
func TestNamedPrimitiveHook(t *testing.T) {
	type Level int
	type Ratio float64
	type Flag bool
	h := codec.NamedPrimitiveHook{}
	for _, tc := range []struct {
		typ  reflect.Type
		in   string
		want any
	}{
		{reflect.TypeFor[Level](), "3", int64(3)}, {reflect.TypeFor[Ratio](), "0.5", 0.5}, {reflect.TypeFor[Flag](), "yes", true},
	} {
		if !h.Match(tc.typ) {
			t.Fatal(tc.typ)
		}
		got, err := h.ConvertWithTarget(tc.in, tc.typ)
		if err != nil || got != tc.want {
			t.Fatalf("%v %v", got, err)
		}
	}
	if _, err := h.ConvertWithTarget("x", reflect.TypeFor[Level]()); err == nil {
		t.Fatal("accepted invalid int")
	}
	if !h.Match(reflect.TypeFor[time.Duration]()) {
		t.Fatal("duration must match fallback")
	}
}
func TestNamedPrimitiveHook_SkipsTextUnmarshaler(t *testing.T) {
	if (codec.NamedPrimitiveHook{}).Match(reflect.TypeFor[textLevel]()) {
		t.Fatal("matched TextUnmarshaler")
	}
}

type namedByte uint8
type customList []string

func (l *customList) UnmarshalText(raw []byte) error { *l = []string{string(raw)}; return nil }
func TestListCoercion_NamedByteAndCustomUnmarshaler(t *testing.T) {
	type C struct {
		Codes  []namedByte `json:"codes"`
		Custom customList  `json:"custom"`
	}
	m := map[string]any{"codes": "1,2", "custom": "a,b"}
	hooks := append(codec.DefaultTypedHooks(), codec.NamedPrimitiveHook{})
	if err := codec.BuildTypedHookPlan(reflect.TypeFor[C](), hooks, codec.WithListCoercion()).Apply(m); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m["codes"], []any{uint64(1), uint64(2)}) || m["custom"] != "a,b" {
		t.Fatalf("%#v", m)
	}
}
