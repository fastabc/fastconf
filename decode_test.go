package fastconf

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"gopkg.in/yaml.v3"
)

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
