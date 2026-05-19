package provider_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/pkg/provider"
)

// TestLabels_RoutingDedupParity_PlainValues verifies behavioral parity between
// LabelProvider and RoutingLabelProvider for plain string values that do not
// trigger scalar coercion or list splitting.
//
// With routing-specific knobs disabled (Raw=true, NoListSplit=true) routing
// keeps all values as raw strings. With Coerce=false (default) the plain
// provider also keeps values as strings. Both should produce identical trees.
func TestLabels_RoutingDedupParity_PlainValues(t *testing.T) {
	in := []string{"app.name=hello", "app.tier=backend"}

	a, err := provider.NewLabels(in, provider.LabelOptions{}).Load(context.Background())
	if err != nil {
		t.Fatalf("plain labels Load: %v", err)
	}
	b, err := provider.NewRoutingLabels(in, provider.RoutingLabelOptions{Raw: true, NoListSplit: true}).Load(context.Background())
	if err != nil {
		t.Fatalf("routing labels Load: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("parity mismatch\nplain:   %#v\nrouting: %#v", a, b)
	}
}
