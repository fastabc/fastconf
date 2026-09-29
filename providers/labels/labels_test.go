package labels_test

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf/internal/testutil"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	provider "github.com/fastabc/fastconf/providers/labels"
)

func TestLabelProvider_ListForm(t *testing.T) {
	p := provider.New([]string{
		"routing.http.services.dummy-svc.loadbalancer.server.port=9999",
		"routing.enable=true",
	}, provider.Options{})
	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := confmap.GetDotted(got, "routing.http.services.dummy-svc.loadbalancer.server.port")
	if port != "9999" {
		t.Fatalf("port got %v", port)
	}
}

func TestLabelProvider_MapForm(t *testing.T) {
	p := provider.New(map[string]string{
		"app.kubernetes.io/name":      "web",
		"app.kubernetes.io/component": "frontend",
	}, provider.Options{Separators: []string{"/"}})
	got, _ := testutil.Map(p.Load(context.Background()))
	// Separator="/" makes "app.kubernetes.io/name" split into
	// ["app.kubernetes.io", "name"] — two segments. GetDotted splits its
	// query on ".", so we use Get with explicit segments instead.
	if v, _ := confmap.Get(got, "app.kubernetes.io", "name"); v != "web" {
		t.Fatalf("name got %v", v)
	}
}

func TestLabelProvider_DefaultPriorityIsStatic(t *testing.T) {
	p := provider.New([]string{"k=v"}, provider.Options{})
	if got := contracts.Describe(p).Priority; got != contracts.PriorityStatic {
		t.Fatalf("priority got %d want PriorityStatic %d", got, contracts.PriorityStatic)
	}
}

func TestLabelProvider_ExplicitK8sPriorityRetained(t *testing.T) {
	p := provider.New([]string{"k=v"}, provider.Options{
		Priority: contracts.PriorityK8s,
	})
	if got := contracts.Describe(p).Priority; got != contracts.PriorityK8s {
		t.Fatalf("priority got %d want PriorityK8s %d", got, contracts.PriorityK8s)
	}
}

func TestLabelProvider_ExplicitCLIPriorityRetained(t *testing.T) {
	p := provider.New([]string{"routing.enable=true"}, provider.Options{
		Priority: contracts.PriorityCLI,
	})
	if got := contracts.Describe(p).Priority; got != contracts.PriorityCLI {
		t.Fatalf("priority got %d want PriorityCLI %d", got, contracts.PriorityCLI)
	}
}

func TestLabelProvider_DottedMapForm(t *testing.T) {
	p := provider.New(map[string]string{"server.addr": ":9090"}, provider.Options{})
	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "server.addr"); v != ":9090" {
		t.Fatalf("server.addr got %v", v)
	}
}

func TestLabelProvider_WatchReturnsNil(t *testing.T) {
	p := provider.New(nil, provider.Options{})
	ch, err := p.Watch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if ch != nil {
		t.Fatal("Watch should return (nil, nil) — labels are static")
	}
}

func TestLabelProvider_NameDefaultIncludesPrefix(t *testing.T) {
	p := provider.New([]string{"x=y"}, provider.Options{Prefix: "routing."})
	if p.Name() != "labels:routing." {
		t.Fatalf("name got %q", p.Name())
	}
}
