package cliflag

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

func TestCLIProvider(t *testing.T) {
	p := NewCLI(map[string]any{"server": map[string]any{"addr": ":9090"}})
	got, _ := p.Load(context.Background())
	if got["server"].(map[string]any)["addr"] != ":9090" {
		t.Error("cli load mismatch")
	}
	if p.Priority() != contracts.PriorityCLI {
		t.Error("priority")
	}
}
