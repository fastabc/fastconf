package labels_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	provider "github.com/fastabc/fastconf/providers/labels"
)

func TestProvider_ListForm(t *testing.T) {
	p := provider.New([]string{
		"routing.http.services.dummy-svc.loadbalancer.server.port=9999",
		"routing.enable=true",
	}, provider.Options{})
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
	if err != nil {
		t.Fatal(err)
	}
	port, _ := confmap.GetDotted(got, "routing.http.services.dummy-svc.loadbalancer.server.port")
	if port != "9999" {
		t.Fatalf("port got %v", port)
	}
}

func TestProvider_MapForm(t *testing.T) {
	p := provider.New(map[string]string{
		"app.kubernetes.io/name":      "web",
		"app.kubernetes.io/component": "frontend",
	}, provider.Options{Separators: []string{"/"}})
	gotSnapshot, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := gotSnapshot.Map
	// Separator="/" makes "app.kubernetes.io/name" split into
	// ["app.kubernetes.io", "name"] — two segments. GetDotted splits its
	// query on ".", so we use Get with explicit segments instead.
	if v, _ := confmap.Get(got, "app.kubernetes.io", "name"); v != "web" {
		t.Fatalf("name got %v", v)
	}
}

func TestProvider_DefaultPriorityIsStatic(t *testing.T) {
	p := provider.New([]string{"k=v"}, provider.Options{})
	if got := contracts.Describe(p).Priority; got != contracts.PriorityStatic {
		t.Fatalf("priority got %d want PriorityStatic %d", got, contracts.PriorityStatic)
	}
}

func TestProvider_ExplicitK8sPriorityRetained(t *testing.T) {
	p := provider.New([]string{"k=v"}, provider.Options{
		Priority: contracts.PriorityK8s,
	})
	if got := contracts.Describe(p).Priority; got != contracts.PriorityK8s {
		t.Fatalf("priority got %d want PriorityK8s %d", got, contracts.PriorityK8s)
	}
}

func TestProvider_ExplicitCLIPriorityRetained(t *testing.T) {
	p := provider.New([]string{"routing.enable=true"}, provider.Options{
		Priority: contracts.PriorityCLI,
	})
	if got := contracts.Describe(p).Priority; got != contracts.PriorityCLI {
		t.Fatalf("priority got %d want PriorityCLI %d", got, contracts.PriorityCLI)
	}
}

func TestProvider_DottedMapForm(t *testing.T) {
	p := provider.New(map[string]string{"server.addr": ":9090"}, provider.Options{})
	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "server.addr"); v != ":9090" {
		t.Fatalf("server.addr got %v", v)
	}
}

func TestProvider_LoadOnly(t *testing.T) {
	p := provider.New(nil, provider.Options{})
	if _, ok := any(p).(contracts.Watcher); ok {
		t.Fatal("load-only provider unexpectedly implements Watcher")
	}
}

func TestProvider_NameDefaultIncludesPrefix(t *testing.T) {
	p := provider.New([]string{"x=y"}, provider.Options{Prefix: "routing."})
	if p.Name() != "labels:routing." {
		t.Fatalf("name got %q", p.Name())
	}
}

func TestProvider_StringValues(t *testing.T) {
	want := map[string]any{"app": map[string]any{"name": "hello", "tier": "backend"}}
	for _, tc := range []struct {
		name string
		opts provider.Options
	}{
		{"plain", provider.Options{}},
		{"routing raw", provider.Options{Routing: &provider.Routing{Raw: true, NoListSplit: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := provider.New([]string{"app.name=hello", "app.tier=backend"}, tc.opts).Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot.Map, want) {
				t.Fatalf("Load = %#v; want %#v", snapshot.Map, want)
			}
		})
	}
}
