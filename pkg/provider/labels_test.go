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
		"routing.http.services.dummy-svc.loadbalancer.server.port=9999",
		"routing.enable=true",
	}, provider.LabelOptions{})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := mappath.GetDotted(got, "routing.http.services.dummy-svc.loadbalancer.server.port")
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

func TestLabelProvider_DefaultPriorityIsStatic(t *testing.T) {
	p := provider.NewLabels([]string{"k=v"}, provider.LabelOptions{})
	if got := p.Priority(); got != contracts.PriorityStatic {
		t.Fatalf("priority got %d want PriorityStatic %d", got, contracts.PriorityStatic)
	}
}

func TestLabelProvider_ExplicitK8sPriorityRetained(t *testing.T) {
	p := provider.NewLabels([]string{"k=v"}, provider.LabelOptions{
		Priority: contracts.PriorityK8s,
	})
	if got := p.Priority(); got != contracts.PriorityK8s {
		t.Fatalf("priority got %d want PriorityK8s %d", got, contracts.PriorityK8s)
	}
}

func TestLabelProvider_ExplicitCLIPriorityRetained(t *testing.T) {
	p := provider.NewLabels([]string{"routing.enable=true"}, provider.LabelOptions{
		Priority: contracts.PriorityCLI,
	})
	if got := p.Priority(); got != contracts.PriorityCLI {
		t.Fatalf("priority got %d want PriorityCLI %d", got, contracts.PriorityCLI)
	}
}

func TestDottedLabelProvider_ListForm(t *testing.T) {
	p := provider.NewDottedLabels([]string{"server.addr=:9090"}, provider.DottedLabelOptions{})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "server.addr"); v != ":9090" {
		t.Fatalf("server.addr got %v", v)
	}
}

func TestDottedLabelProvider_MapForm(t *testing.T) {
	p := provider.NewDottedLabelMap(map[string]string{"server.addr": ":9090"}, provider.DottedLabelOptions{})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "server.addr"); v != ":9090" {
		t.Fatalf("server.addr got %v", v)
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
	p := provider.NewLabels([]string{"x=y"}, provider.LabelOptions{Prefix: "routing."})
	if p.Name() != "labels:routing." {
		t.Fatalf("name got %q", p.Name())
	}
}
