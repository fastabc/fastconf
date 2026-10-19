package fastconf

import "testing"

func TestIsUnknownField_RealDecoders(t *testing.T) {
	for _, yaml := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			tree map[string]any
			want bool
		}{
			{"unknown", map[string]any{"port": 1, "prot": 2}, true},
			{"type mismatch", map[string]any{"port": "bad"}, false},
			{"valid", map[string]any{"port": 1}, false},
		} {
			var target struct {
				Port int `json:"port" yaml:"port"`
			}
			err := decodeInto(tc.tree, &target, yaml, true)
			if tc.name != "valid" && err == nil {
				t.Fatalf("%s (yaml=%v) succeeded", tc.name, yaml)
			}
			if got := isUnknownField(err); got != tc.want {
				t.Fatalf("%s (yaml=%v): isUnknownField(%v) = %v, want %v", tc.name, yaml, err, got, tc.want)
			}
		}
	}
}
