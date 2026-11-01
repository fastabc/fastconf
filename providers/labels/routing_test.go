package labels_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	provider "github.com/fastabc/fastconf/providers/labels"
)

func TestRouting_TypedLeavesListsAndIndexes(t *testing.T) {
	p := provider.New([]string{
		"routing.enable=true",
		"routing.http.services.api.loadbalancer.server.port=8080",
		"routing.http.routers.api.entrypoints=web,websecure",
		"routing.http.routers.api.tls.domains[0].main=example.com",
		"routing.http.routers.api.tls.domains[0].sans=www.example.com,api.example.com",
	}, provider.Options{Routing: &provider.Routing{}})

	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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

func TestRouting_ListOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		labels  []string
		options provider.Routing
		want    map[string]any
	}{
		{"Routing_RawSuffixesProtectExpressions", []string{"routing.http.routers.api.rule=Host(`a.example`,`b.example`)", "routing.http.middlewares.api.headers.headersregexp=foo,bar"}, provider.Routing{}, map[string]any{"routing.http.routers.api.rule": "Host(`a.example`,`b.example`)", "routing.http.middlewares.api.headers.headersregexp": "foo,bar"}},
		{"Routing_ExplicitEmptyRawSuffixesDisableProtection", []string{"routing.http.routers.api.rule=a,b"}, provider.Routing{KeepRawSuffixes: []string{}}, map[string]any{"routing.http.routers.api.rule": []any{"a", "b"}}},
		{"Routing_EnableGateSkipsDisabledSet", []string{"routing.enable=false", "routing.http.routers.api.entrypoints=web,websecure"}, provider.Routing{EnableGate: "routing.enable"}, nil},
		{"Routing_EnableGateAllowsAbsentOrTruthySet/absent", []string{"routing.http.routers.api.priority=10"}, provider.Routing{EnableGate: "routing.enable"}, map[string]any{"routing.http.routers.api.priority": int64(10)}},
		{"Routing_EnableGateAllowsAbsentOrTruthySet/truthy", []string{"routing.enable=on", "routing.http.routers.api.priority=10"}, provider.Routing{EnableGate: "routing.enable"}, map[string]any{"routing.http.routers.api.priority": int64(10)}},
		{"Routing_LowercaseKeysNormalizesGateAndTree", []string{"Routing.Enable=true", "Routing.HTTP.Routers.API.EntryPoints=web,websecure"}, provider.Routing{EnableGate: "Routing.Enable", LowercaseKeys: true}, map[string]any{"routing.http.routers.api.entrypoints": []any{"web", "websecure"}}},
		{"Routing_RawAndNoListSplitOptOuts/raw", []string{"routing.enable=true", "routing.http.routers.api.entrypoints=web,websecure"}, provider.Routing{Raw: true}, map[string]any{"routing.enable": "true", "routing.http.routers.api.entrypoints": "web,websecure"}},
		{"Routing_RawAndNoListSplitOptOuts/no-list-split", []string{"routing.http.routers.api.entrypoints=web,websecure"}, provider.Routing{NoListSplit: true}, map[string]any{"routing.http.routers.api.entrypoints": "web,websecure"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := provider.New(tc.labels, provider.Options{Routing: &tc.options}).Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil && len(snap.Map) != 0 {
				t.Fatalf("disabled set=%v", snap.Map)
			}
			for path, want := range tc.want {
				if got, ok := confmap.GetDotted(snap.Map, path); !ok || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s=%#v present=%v want=%#v", path, got, ok, want)
				}
			}
		})
	}
}

func TestRouting_IndexedPromotionLeavesMixedBaseUntouched(t *testing.T) {
	p := provider.New([]string{
		"routing.domains=base",
		"routing.domains[0].main=example.com",
	}, provider.Options{Routing: &provider.Routing{}})

	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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

func TestRouting_SparseIndicesFillWithNil(t *testing.T) {
	treeSnapshot, err := provider.New([]string{"domains[1].main=b.example"}, provider.Options{Routing: &provider.Routing{}}).Load(context.Background())
	tree := treeSnapshot.Map
	if err != nil {
		t.Fatal(err)
	}
	want := []any{nil, map[string]any{"main": "b.example"}}
	if !reflect.DeepEqual(tree["domains"], want) {
		t.Fatalf("domains got %#v want %#v", tree["domains"], want)
	}
}

func TestRouting_MapFormAndDefaults(t *testing.T) {
	p := provider.New(map[string]string{
		"routing.http.routers.api.priority": "10",
	}, provider.Options{Prefix: "routing.", Routing: &provider.Routing{}})

	gotSnapshot, err := p.Load(context.Background())
	got := gotSnapshot.Map
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

func TestRouting_LoadOnly(t *testing.T) {
	p := provider.New(nil, provider.Options{Routing: &provider.Routing{}})
	if _, ok := any(p).(contracts.Watcher); ok {
		t.Fatal("load-only provider unexpectedly implements Watcher")
	}
}

func TestRouting_IndexLimit(t *testing.T) {
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
	treeSnapshot, err := provider.New([]string{"items[1023]=x"}, provider.Options{Routing: &provider.Routing{}}).Load(context.Background())
	tree := treeSnapshot.Map
	if err != nil {
		t.Fatal(err)
	}
	if items := tree["items"].([]any); len(items) != provider.MaxRoutingIndex+1 || items[1023] != "x" {
		t.Fatalf("items len = %d", len(items))
	}
}

func TestRouting_KeyContainingEqualsMatchesPlainMode(t *testing.T) {
	in := map[string]string{"weird=key.name": "v"}
	plain, err := provider.New(in, provider.Options{}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	routed, err := provider.New(in, provider.Options{Routing: &provider.Routing{}}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain.Map, routed.Map) {
		t.Fatalf("routing %v != plain %v", routed.Map, plain.Map)
	}
}

func TestRouting_LabelPairsPreserveInputAndGateOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gates   []string
		blocked bool
	}{
		{"absent", nil, false},
		{"last gate enables", []string{"false", "true"}, false},
		{"last gate disables", []string{"true", "false"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pairs := []confmap.LabelPair{{Key: "Routing.Backend.Port", Value: "8080"}}
			for _, value := range tc.gates {
				pairs = append(pairs, confmap.LabelPair{Key: "Routing.Enable", Value: value})
			}
			before := append([]confmap.LabelPair(nil), pairs...)
			snapshot, err := provider.New(pairs, provider.Options{Routing: &provider.Routing{EnableGate: "Routing.Enable", LowercaseKeys: true}}).Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pairs, before) {
				t.Fatalf("caller input changed: %v", pairs)
			}
			if tc.blocked {
				if len(snapshot.Map) != 0 {
					t.Fatalf("disabled set=%v", snapshot.Map)
				}
				return
			}
			if got, ok := confmap.GetDotted(snapshot.Map, "routing.backend.port"); !ok || got != int64(8080) {
				t.Fatalf("port=%v present=%v", got, ok)
			}
		})
	}
}
