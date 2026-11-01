package labels_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	provider "github.com/fastabc/fastconf/providers/labels"
)

func TestProvider_InputForms(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input any
		opts  provider.Options
		path  []string
		want  any
	}{
		{"Provider_ListForm", []string{"routing.http.services.dummy-svc.loadbalancer.server.port=9999", "routing.enable=true"}, provider.Options{}, []string{"routing", "http", "services", "dummy-svc", "loadbalancer", "server", "port"}, "9999"},
		// Explicit segments preserve dots within Kubernetes metadata keys.
		{"Provider_MapForm", map[string]string{"app.kubernetes.io/name": "web", "app.kubernetes.io/component": "frontend"}, provider.Options{Separators: []string{"/"}}, []string{"app.kubernetes.io", "name"}, "web"},
		{"Provider_DottedMapForm", map[string]string{"server.addr": ":9090"}, provider.Options{}, []string{"server", "addr"}, ":9090"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := provider.New(tc.input, tc.opts).Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := confmap.Get(snap.Map, tc.path...); !ok || got != tc.want {
				t.Fatalf("value=%v present=%v want=%v", got, ok, tc.want)
			}
		})
	}
}

func TestProvider_Priority(t *testing.T) {
	for _, tc := range []struct {
		name           string
		priority, want int
	}{
		{"Provider_DefaultPriorityIsStatic", 0, contracts.PriorityStatic},
		{"Provider_ExplicitK8sPriorityRetained", contracts.PriorityK8s, contracts.PriorityK8s},
		{"Provider_ExplicitCLIPriorityRetained", contracts.PriorityCLI, contracts.PriorityCLI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := provider.New([]string{"k=v"}, provider.Options{Priority: tc.priority})
			if got := contracts.Describe(p).Priority; got != tc.want {
				t.Fatalf("priority=%d want=%d", got, tc.want)
			}
		})
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
