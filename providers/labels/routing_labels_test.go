package labels_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/internal/testutil"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	provider "github.com/fastabc/fastconf/providers/labels"
)

func TestRoutingLabelProvider_TypedLeavesListsAndIndexes(t *testing.T) {
	p := provider.New([]string{
		"routing.enable=true",
		"routing.http.services.api.loadbalancer.server.port=8080",
		"routing.http.routers.api.entrypoints=web,websecure",
		"routing.http.routers.api.tls.domains[0].main=example.com",
		"routing.http.routers.api.tls.domains[0].sans=www.example.com,api.example.com",
	}, provider.Options{Routing: &provider.Routing{}})

	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "routing.enable"); v != true {
		t.Fatalf("routing.enable got %v (%T)", v, v)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.services.api.loadbalancer.server.port"); v != int64(8080) {
		t.Fatalf("port got %v (%T)", v, v)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.routers.api.entrypoints"); !reflect.DeepEqual(v, []any{"web", "websecure"}) {
		t.Fatalf("entrypoints got %#v", v)
	}

	domains, ok := confmap.GetDotted(got, "routing.http.routers.api.tls.domains")
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
	p := provider.New([]string{
		"routing.http.routers.api.rule=Host(`a.example`,`b.example`)",
		"routing.http.middlewares.api.headers.headersregexp=foo,bar",
	}, provider.Options{Routing: &provider.Routing{}})

	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.routers.api.rule"); v != "Host(`a.example`,`b.example`)" {
		t.Fatalf("rule got %#v", v)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.middlewares.api.headers.headersregexp"); v != "foo,bar" {
		t.Fatalf("headersregexp got %#v", v)
	}
}

func TestRoutingLabelProvider_ExplicitEmptyRawSuffixesDisableProtection(t *testing.T) {
	p := provider.New([]string{
		"routing.http.routers.api.rule=a,b",
	}, provider.Options{Routing: &provider.Routing{KeepRawSuffixes: []string{}}})

	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.routers.api.rule"); !reflect.DeepEqual(v, []any{"a", "b"}) {
		t.Fatalf("rule got %#v", v)
	}
}

func TestRoutingLabelProvider_EnableGateSkipsDisabledSet(t *testing.T) {
	p := provider.New([]string{
		"routing.enable=false",
		"routing.http.routers.api.entrypoints=web,websecure",
	}, provider.Options{Routing: &provider.Routing{EnableGate: "routing.enable"}})

	got, err := testutil.Map(p.Load(context.Background()))
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
			p := provider.New(tc.labels, provider.Options{Routing: &provider.Routing{EnableGate: "routing.enable"}})
			got, err := testutil.Map(p.Load(context.Background()))
			if err != nil {
				t.Fatal(err)
			}
			if v, _ := confmap.GetDotted(got, "routing.http.routers.api.priority"); v != int64(10) {
				t.Fatalf("priority got %#v", v)
			}
		})
	}
}

func TestRoutingLabelProvider_LowercaseKeysNormalizesGateAndTree(t *testing.T) {
	p := provider.New([]string{
		"Routing.Enable=true",
		"Routing.HTTP.Routers.API.EntryPoints=web,websecure",
	}, provider.Options{Routing: &provider.Routing{
		EnableGate:    "Routing.Enable",
		LowercaseKeys: true,
	}})

	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.routers.api.entrypoints"); !reflect.DeepEqual(v, []any{"web", "websecure"}) {
		t.Fatalf("normalized entrypoints got %#v", v)
	}
}

func TestRoutingLabelProvider_RawAndNoListSplitOptOuts(t *testing.T) {
	t.Run("raw", func(t *testing.T) {
		p := provider.New([]string{
			"routing.enable=true",
			"routing.http.routers.api.entrypoints=web,websecure",
		}, provider.Options{Routing: &provider.Routing{Raw: true}})
		got, err := testutil.Map(p.Load(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := confmap.GetDotted(got, "routing.enable"); v != "true" {
			t.Fatalf("raw enable got %#v", v)
		}
		if v, _ := confmap.GetDotted(got, "routing.http.routers.api.entrypoints"); v != "web,websecure" {
			t.Fatalf("raw entrypoints got %#v", v)
		}
	})

	t.Run("no-list-split", func(t *testing.T) {
		p := provider.New([]string{
			"routing.http.routers.api.entrypoints=web,websecure",
		}, provider.Options{Routing: &provider.Routing{NoListSplit: true}})
		got, err := testutil.Map(p.Load(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := confmap.GetDotted(got, "routing.http.routers.api.entrypoints"); v != "web,websecure" {
			t.Fatalf("entrypoints got %#v", v)
		}
	})
}

func TestRoutingLabelProvider_IndexedPromotionLeavesMixedBaseUntouched(t *testing.T) {
	p := provider.New([]string{
		"routing.domains=base",
		"routing.domains[0].main=example.com",
	}, provider.Options{Routing: &provider.Routing{}})

	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "routing.domains"); v != "base" {
		t.Fatalf("base domains got %#v", v)
	}
	if v, _ := confmap.Get(got, "routing", "domains[0]", "main"); v != "example.com" {
		t.Fatalf("indexed sibling should remain untouched, got %#v", v)
	}
}

func TestRoutingLabelProvider_SparseIndicesFillWithNil(t *testing.T) {
	tree, err := testutil.Map(provider.New([]string{"domains[1].main=b.example"}, provider.Options{Routing: &provider.Routing{}}).Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{nil, map[string]any{"main": "b.example"}}
	if !reflect.DeepEqual(tree["domains"], want) {
		t.Fatalf("domains got %#v want %#v", tree["domains"], want)
	}
}

func TestRoutingLabelProvider_MapFormAndDefaults(t *testing.T) {
	p := provider.New(map[string]string{
		"routing.http.routers.api.priority": "10",
	}, provider.Options{Prefix: "routing.", Routing: &provider.Routing{}})

	got, err := testutil.Map(p.Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := confmap.GetDotted(got, "routing.http.routers.api.priority"); v != int64(10) {
		t.Fatalf("priority got %#v", v)
	}
	if contracts.Describe(p).Priority != contracts.PriorityStatic {
		t.Fatalf("priority got %d want %d", contracts.Describe(p).Priority, contracts.PriorityStatic)
	}
	if p.Name() != "labels:routing:routing." {
		t.Fatalf("name got %q", p.Name())
	}
}

func TestRoutingLabelProvider_WatchReturnsNil(t *testing.T) {
	p := provider.New(nil, provider.Options{Routing: &provider.Routing{}})
	ch, err := p.Watch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if ch != nil {
		t.Fatal("Watch should return (nil, nil) — labels are static")
	}
}

func TestRoutingLabelProvider_IndexLimit(t *testing.T) {
	for _, key := range []string{
		"items[9223372036854775807]=x",
		"items[99999999999999999999]=x",
		"a.items[1024]=x",
	} {
		_, err := provider.New([]string{key}, provider.Options{Routing: &provider.Routing{}}).Load(context.Background())
		if err == nil || !strings.Contains(err.Error(), "index exceeds") {
			t.Errorf("%s: err = %v, want index limit error", key, err)
		}
	}
	tree, err := testutil.Map(provider.New([]string{"items[1023]=x"}, provider.Options{Routing: &provider.Routing{}}).Load(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if items := tree["items"].([]any); len(items) != provider.MaxRoutingIndex+1 || items[1023] != "x" {
		t.Fatalf("items len = %d", len(items))
	}
}
