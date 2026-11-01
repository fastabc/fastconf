package fastconf

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/providers/source"

	"gopkg.in/yaml.v3"
)

func TestYAMLBridge_FieldNamedInlineKeepsTypedHooks(t *testing.T) {
	type sub struct {
		Port int `yaml:"port"`
	}
	t.Run("struct", func(t *testing.T) {
		type cfg struct {
			Inline sub `yaml:"inline"`
		}
		st, err := Load[cfg](context.Background(), WithFS(fstest.MapFS{}), WithDecodeFormat(YAML),
			WithProvider(source.NewBytes("in", "json", []byte(`{"inline":{"port":"8080"}}`))))
		if err != nil {
			t.Fatal(err)
		}
		if st.Value().Inline.Port != 8080 {
			t.Fatalf("value=%+v", st.Value())
		}
	})
	t.Run("map", func(t *testing.T) {
		type cfg struct {
			Inline map[string]int `yaml:"inline"`
		}
		st, err := Load[cfg](context.Background(), WithFS(fstest.MapFS{}), WithDecodeFormat(YAML),
			WithProvider(source.NewBytes("in", "json", []byte(`{"inline":{"a":"8"}}`))))
		if err != nil {
			t.Fatal(err)
		}
		if st.Value().Inline["a"] != 8 {
			t.Fatalf("value=%+v", st.Value())
		}
	})
}

type countingBridgeValue struct{ calls *int }

func (v countingBridgeValue) MarshalJSON() ([]byte, error) {
	*v.calls++
	return []byte("7"), nil
}

func (v countingBridgeValue) MarshalYAML() (any, error) {
	*v.calls++
	return 7, nil
}

type accumulatingBridgeValue []int

func (v *accumulatingBridgeValue) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = append(*v, n)
	return nil
}

func (v *accumulatingBridgeValue) UnmarshalYAML(node *yaml.Node) error {
	var n int
	if err := node.Decode(&n); err != nil {
		return err
	}
	*v = append(*v, n)
	return nil
}

func TestUnknownWarnEncodesOnceAndRetriesFreshTarget(t *testing.T) {
	type config struct {
		Known accumulatingBridgeValue `json:"known" yaml:"known"`
	}
	for _, yamlBridge := range []bool{false, true} {
		calls := 0
		m := &Manager[config]{opts: options{UnknownFields: UnknownWarn, YAMLBridge: yamlBridge,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
		p := &pipelineState[config]{merged: map[string]any{"known": countingBridgeValue{&calls}, "unknown": true}}
		if err := runDecode(context.Background(), m, p); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || len(p.target.Known) != 1 || p.target.Known[0] != 7 {
			t.Fatalf("yaml=%v: encodes=%d, decoded=%v", yamlBridge, calls, p.target.Known)
		}
		p.merged["known"] = "invalid"
		if err := runDecode(context.Background(), m, p); err == nil {
			t.Fatalf("yaml=%v: retry hid type error", yamlBridge)
		}
	}
}

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
			err := decodeTestInput(tc.tree, &target, yaml, true)
			if tc.name != "valid" && err == nil {
				t.Fatalf("%s (yaml=%v) succeeded", tc.name, yaml)
			}
			if got := isUnknownField(err); got != tc.want {
				t.Fatalf("%s (yaml=%v): isUnknownField(%v) = %v, want %v", tc.name, yaml, err, got, tc.want)
			}
		}
	}
}

// decodeTestInput exercises the production encode/decode bridge for one test input.
func decodeTestInput[T any](m map[string]any, target *T, yamlBridge, strict bool) error {
	input, err := encodeInput(m, yamlBridge)
	if err != nil {
		return err
	}
	defer input.release()
	return input.decode(target, strict)
}
