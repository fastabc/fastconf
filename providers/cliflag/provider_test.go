package cliflag

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

func TestProvider_Load(t *testing.T) {
	p := New(map[string]any{"server": map[string]any{"addr": ":9090"}})
	gotSnapshot, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := gotSnapshot.Map
	if got["server"].(map[string]any)["addr"] != ":9090" {
		t.Error("cli load mismatch")
	}
	if contracts.Describe(p).Priority != contracts.PriorityCLI {
		t.Error("priority")
	}
}
