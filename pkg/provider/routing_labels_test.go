package provider_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
	"github.com/fastabc/fastconf/pkg/provider"
)

func TestRoutingLabelProvider_TypedLeavesListsAndIndexes(t *testing.T) {
	p := provider.NewRoutingLabels([]string{
		"routing.enable=true",
		"routing.http.services.api.loadbalancer.server.port=8080",
		"routing.http.routers.api.entrypoints=web,websecure",
		"routing.http.routers.api.tls.domains[0].main=example.com",
		"routing.http.routers.api.tls.domains[0].sans=www.example.com,api.example.com",
	}, provider.RoutingLabelOptions{})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "routing.enable"); v != true {
		t.Fatalf("routing.enable got %v (%T)", v, v)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.services.api.loadbalancer.server.port"); v != int64(8080) {
		t.Fatalf("port got %v (%T)", v, v)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.routers.api.entrypoints"); !reflect.DeepEqual(v, []any{"web", "websecure"}) {
		t.Fatalf("entrypoints got %#v", v)
	}

	domains, ok := mappath.GetDotted(got, "routing.http.routers.api.tls.domains")
	if !ok {
		t.Fatal("domains missing")
	}
	wantDomains := []any{
		map[string]any{
			"main": "example.com",
			"sans": []any{"www.example.com", "api.example.com"},
		},
	}
	if !reflect.DeepEqual(domains, wantDomains) {
		t.Fatalf("domains got %#v want %#v", domains, wantDomains)
	}
}

func TestRoutingLabelProvider_RawSuffixesProtectExpressions(t *testing.T) {
	p := provider.NewRoutingLabels([]string{
		"routing.http.routers.api.rule=Host(`a.example`,`b.example`)",
		"routing.http.middlewares.api.headers.headersregexp=foo,bar",
	}, provider.RoutingLabelOptions{})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.routers.api.rule"); v != "Host(`a.example`,`b.example`)" {
		t.Fatalf("rule got %#v", v)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.middlewares.api.headers.headersregexp"); v != "foo,bar" {
		t.Fatalf("headersregexp got %#v", v)
	}
}

func TestRoutingLabelProvider_ExplicitEmptyRawSuffixesDisableProtection(t *testing.T) {
	p := provider.NewRoutingLabels([]string{
		"routing.http.routers.api.rule=a,b",
	}, provider.RoutingLabelOptions{KeepRawSuffixes: []string{}})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.routers.api.rule"); !reflect.DeepEqual(v, []any{"a", "b"}) {
		t.Fatalf("rule got %#v", v)
	}
}

func TestRoutingLabelProvider_EnableGateSkipsDisabledSet(t *testing.T) {
	p := provider.NewRoutingLabels([]string{
		"routing.enable=false",
		"routing.http.routers.api.entrypoints=web,websecure",
	}, provider.RoutingLabelOptions{EnableGate: "routing.enable"})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("disabled set should be empty, got %#v", got)
	}
}

func TestRoutingLabelProvider_EnableGateAllowsAbsentOrTruthySet(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
	}{
		{name: "absent", labels: []string{"routing.http.routers.api.priority=10"}},
		{name: "truthy", labels: []string{"routing.enable=on", "routing.http.routers.api.priority=10"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := provider.NewRoutingLabels(tc.labels, provider.RoutingLabelOptions{EnableGate: "routing.enable"})
			got, err := p.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if v, _ := mappath.GetDotted(got, "routing.http.routers.api.priority"); v != int64(10) {
				t.Fatalf("priority got %#v", v)
			}
		})
	}
}

func TestRoutingLabelProvider_LowercaseKeysNormalizesGateAndTree(t *testing.T) {
	p := provider.NewRoutingLabels([]string{
		"Routing.Enable=true",
		"Routing.HTTP.Routers.API.EntryPoints=web,websecure",
	}, provider.RoutingLabelOptions{
		EnableGate:    "Routing.Enable",
		LowercaseKeys: true,
	})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.routers.api.entrypoints"); !reflect.DeepEqual(v, []any{"web", "websecure"}) {
		t.Fatalf("normalized entrypoints got %#v", v)
	}
}

func TestRoutingLabelProvider_RawAndNoListSplitOptOuts(t *testing.T) {
	t.Run("raw", func(t *testing.T) {
		p := provider.NewRoutingLabels([]string{
			"routing.enable=true",
			"routing.http.routers.api.entrypoints=web,websecure",
		}, provider.RoutingLabelOptions{Raw: true})
		got, err := p.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := mappath.GetDotted(got, "routing.enable"); v != "true" {
			t.Fatalf("raw enable got %#v", v)
		}
		if v, _ := mappath.GetDotted(got, "routing.http.routers.api.entrypoints"); v != "web,websecure" {
			t.Fatalf("raw entrypoints got %#v", v)
		}
	})

	t.Run("no-list-split", func(t *testing.T) {
		p := provider.NewRoutingLabels([]string{
			"routing.http.routers.api.entrypoints=web,websecure",
		}, provider.RoutingLabelOptions{NoListSplit: true})
		got, err := p.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := mappath.GetDotted(got, "routing.http.routers.api.entrypoints"); v != "web,websecure" {
			t.Fatalf("entrypoints got %#v", v)
		}
	})
}

func TestRoutingLabelProvider_IndexedPromotionLeavesMixedBaseUntouched(t *testing.T) {
	p := provider.NewRoutingLabels([]string{
		"routing.domains=base",
		"routing.domains[0].main=example.com",
	}, provider.RoutingLabelOptions{})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "routing.domains"); v != "base" {
		t.Fatalf("base domains got %#v", v)
	}
	if v, _ := mappath.Get(got, "routing", "domains[0]", "main"); v != "example.com" {
		t.Fatalf("indexed sibling should remain untouched, got %#v", v)
	}
}

func TestPromoteIndexedRoutingKeys_SparseIndicesFillWithNil(t *testing.T) {
	tree := map[string]any{
		"domains[1]": map[string]any{"main": "b.example"},
	}
	provider.PromoteIndexedRoutingKeys(tree)
	want := []any{nil, map[string]any{"main": "b.example"}}
	if !reflect.DeepEqual(tree["domains"], want) {
		t.Fatalf("domains got %#v want %#v", tree["domains"], want)
	}
}

func TestRoutingLabelProvider_MapFormAndDefaults(t *testing.T) {
	p := provider.NewRoutingLabelMap(map[string]string{
		"routing.http.routers.api.priority": "10",
	}, provider.RoutingLabelOptions{Prefix: "routing."})

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mappath.GetDotted(got, "routing.http.routers.api.priority"); v != int64(10) {
		t.Fatalf("priority got %#v", v)
	}
	if p.Priority() != contracts.PriorityStatic {
		t.Fatalf("priority got %d want %d", p.Priority(), contracts.PriorityStatic)
	}
	if p.Name() != "labels:routing:routing." {
		t.Fatalf("name got %q", p.Name())
	}
}

func TestRoutingLabelProvider_WatchReturnsNil(t *testing.T) {
	p := provider.NewRoutingLabels(nil, provider.RoutingLabelOptions{})
	ch, err := p.Watch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ch != nil {
		t.Fatal("Watch should return (nil, nil) — labels are static")
	}
}
