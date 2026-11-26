package fastconf

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/providers/source"
	goyaml "go.yaml.in/yaml/v3"
	"gopkg.in/yaml.v3"
)

// foreignNodeHook implements the node hook of go.yaml.in/yaml/v3, which the
// bridge (gopkg.in/yaml.v3) never calls.
type foreignNodeHook struct{ Host string }

func (h *foreignNodeHook) UnmarshalYAML(n *goyaml.Node) error { return n.Decode(&h.Host) }

// bridgeNodeHook implements the node hook the bridge does call.
type bridgeNodeHook struct{ Host string }

func (h *bridgeNodeHook) UnmarshalYAML(n *yaml.Node) error { return n.Decode(&h.Host) }

type foreignHookConfig struct {
	DB struct {
		Addr foreignNodeHook `yaml:"addr"`
	} `yaml:"db"`
}

func TestYAMLBridgeRejectsForeignNodeHooks(t *testing.T) {
	// A mapping decodes field by field when the hook is skipped, so without
	// the check the mistake passes silently.
	src := WithProvider(source.NewBytes("in", "json", []byte(`{"db":{"addr":{"host":"x"}}}`)))
	_, err := Load[foreignHookConfig](context.Background(), WithFS(fstest.MapFS{}), WithDecodeFormat(YAML), src)
	if !errors.Is(err, ErrFastConf) {
		t.Fatalf("Load = %v; want construction error", err)
	}
	for _, want := range []string{"db.addr", "go.yaml.in/yaml/v3", "gopkg.in/yaml.v3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	// The JSON bridge never calls YAML hooks, so it has nothing to reject.
	if _, err := Load[foreignHookConfig](context.Background(), WithFS(fstest.MapFS{}), src); err != nil {
		t.Fatalf("JSON bridge Load = %v", err)
	}

	type ok struct {
		Addr bridgeNodeHook `yaml:"addr"`
	}
	st, err := Load[ok](context.Background(), WithFS(fstest.MapFS{}), WithDecodeFormat(YAML),
		WithProvider(source.NewBytes("in", "json", []byte(`{"addr":"y"}`))))
	if err != nil {
		t.Fatalf("bridge hook Load = %v", err)
	}
	if st.Value().Addr.Host != "y" {
		t.Fatalf("bridge hook not called: %+v", st.Value())
	}
}

func TestCustomEncodingCoversEveryYAMLLibrary(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[foreignHookConfig](), reflect.TypeFor[bridgeNodeHook]()} {
		if !hasCustomEncoding(typ) {
			t.Errorf("hasCustomEncoding(%v) = false; want true", typ)
		}
	}
	if hasCustomEncoding(reflect.TypeFor[snapshotConfig]()) {
		t.Error("hasCustomEncoding(snapshotConfig) = true; want false")
	}
}
