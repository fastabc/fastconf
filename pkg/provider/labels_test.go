package provider_test

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
	"github.com/fastabc/fastconf/pkg/provider"
)

func TestLabelProvider_ListForm(t *testing.T) {
	p := provider.NewLabels([]string{
		"traefik.http.services.dummy-svc.loadbalancer.server.port=9999",
		"traefik.enable=true",
	}, provider.LabelOptions{})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := mappath.GetDotted(got, "traefik.http.services.dummy-svc.loadbalancer.server.port")
	if port != "9999" {
		t.Fatalf("port got %v", port)
	}
}

func TestLabelProvider_MapForm(t *testing.T) {
	p := provider.NewLabelMap(map[string]string{
		"app.kubernetes.io/name":      "web",
		"app.kubernetes.io/component": "frontend",
	}, provider.LabelOptions{Separator: "/"})
	got, _ := p.Load(context.Background())
	// Separator="/" makes "app.kubernetes.io/name" split into
	// ["app.kubernetes.io", "name"] — two segments. GetDotted splits its
	// query on ".", so we use Get with explicit segments instead.
	if v, _ := mappath.Get(got, "app.kubernetes.io", "name"); v != "web" {
		t.Fatalf("name got %v", v)
	}
}

func TestLabelProvider_DefaultPriorityIsCLI(t *testing.T) {
	p := provider.NewLabels([]string{"k=v"}, provider.LabelOptions{})
	if got := p.Priority(); got != contracts.PriorityCLI {
		t.Fatalf("priority got %d want PriorityCLI %d", got, contracts.PriorityCLI)
	}
}

func TestLabelProvider_WatchReturnsNil(t *testing.T) {
	p := provider.NewLabels(nil, provider.LabelOptions{})
	ch, err := p.Watch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ch != nil {
		t.Fatal("Watch should return (nil, nil) — labels are static")
	}
}

func TestLabelProvider_NameDefaultIncludesPrefix(t *testing.T) {
	p := provider.NewLabels([]string{"x=y"}, provider.LabelOptions{Prefix: "traefik."})
	if p.Name() != "labels:traefik." {
		t.Fatalf("name got %q", p.Name())
	}
}
