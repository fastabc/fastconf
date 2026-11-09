package fastconf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSecretMetadataCommitLifecycle(t *testing.T) {
	ctx := context.Background()
	fs := fstest.MapFS{"conf.d/base/a.yaml": {Data: []byte("a: canary\nb: second\n")}}
	var reject bool
	var commits []Committed
	m, err := New[map[string]string](ctx, WithFS(fs), WithHistory(8), WithProvenance(ProvenanceFull),
		WithSecretResolvers(PrefixSecretResolver("enc", "enc:", func(_ context.Context, body string) (string, error) { return body, nil })),
		WithValidate(func(*map[string]string) error {
			if reject {
				return errors.New("rejected")
			}
			return nil
		}), WithObserver(observerFunc(func(_ context.Context, e Event) {
			if c, ok := e.(Committed); ok {
				commits = append(commits, c)
			}
		})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	var callbacks int
	Subscribe(m, func(c *map[string]string) *map[string]string { return c }, func(_, _ *map[string]string) { callbacks++ }, WithEqual(func(_, _ *map[string]string) bool { return false }))
	before := m.Snapshot()
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("a: enc:canary\nb: enc:second\n")}
	plan, err := m.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Proposed.Map()["a"] != "***REDACTED***" || len(plan.Diff) != 0 || m.Snapshot() != before {
		t.Fatal("preview must mask secrets without publishing or inventing a value diff")
	}
	reject = true
	if err := m.Reload(ctx); err == nil || m.Snapshot() != before {
		t.Fatal("failed reload published metadata")
	}
	reject = false
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	masked := m.Snapshot()
	if masked == before || masked.Generation() != before.Generation()+1 || masked.Hash() != before.Hash() {
		t.Fatal("security metadata must publish a new generation with the same value hash")
	}
	if callbacks != 0 || len(commits) != 2 || len(commits[1].Diff()) != 0 || m.History().List()[0] != before {
		t.Fatal("metadata commit must update history/audit without invoking value subscribers")
	}
	if before.Map()["a"] != "canary" || masked.Unredacted().Map()["a"] != "canary" {
		t.Fatal("snapshot payload was mutated")
	}
	data, err := masked.Dump(JSON)
	if err != nil || strings.Contains(string(data), "canary") {
		t.Fatalf("dump leaked a secret: %s (%v)", data, err)
	}
	for _, origin := range masked.Explain("a") {
		if origin.Value != nil && origin.Value != "***REDACTED***" {
			t.Fatalf("unmasked origin: %+v", origin)
		}
	}
	for range 20 {
		if err := m.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		if m.Snapshot() != masked {
			t.Fatal("map iteration order created a spurious metadata commit")
		}
	}
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("a: canary\nb: second\n")}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Map()["a"] != "canary" || callbacks != 0 {
		t.Fatal("metadata removal did not preserve value subscriber semantics")
	}
	if err := m.History().Rollback(masked); err != nil {
		t.Fatal(err)
	}
	rolled := m.Snapshot()
	if rolled.Map()["a"] != "***REDACTED***" {
		t.Fatal("rollback lost resolved paths")
	}
	fs["conf.d/base/a.yaml"] = &fstest.MapFile{Data: []byte("a: enc:canary\nb: enc:second\n")}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != rolled {
		t.Fatal("rollback failed to retain metadata identity")
	}
}
