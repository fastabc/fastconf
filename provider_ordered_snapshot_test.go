package fastconf_test

// WithProviderOrdered wraps providers in a priority override. The wrapper
// must keep forwarding the SnapshotProvider capability: method promotion
// through an embedded interface value does not satisfy
// contracts.SnapshotProvider, so a provider's Revision/Stale would be
// silently dropped unless the wrapper declares LoadSnapshot explicitly.

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
)

// snapshotProvider returns a revisioned snapshot via LoadSnapshot.
type snapshotProvider struct {
	name string
	prio int
	rev  string
	data map[string]any
}

func (p *snapshotProvider) Name() string  { return p.name }
func (p *snapshotProvider) Priority() int { return p.prio }
func (p *snapshotProvider) Load(context.Context) (map[string]any, error) {
	return p.data, nil
}
func (p *snapshotProvider) LoadSnapshot(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{Map: p.data, Revision: p.rev}, nil
}
func (p *snapshotProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

func TestProviderOrderedPreservesSnapshotRevision(t *testing.T) {
	p := &snapshotProvider{
		name: "etcdish",
		// Priority must be 0 for WithProviderOrdered to assign one.
		rev:  "rev-42",
		data: map[string]any{"db": map[string]any{"host": "ordered"}},
	}
	mgr, err := fastconf.New[ownershipCfg](context.Background(),
		fastconf.WithFS(newFS(nil)),
		fastconf.WithDir("conf.d"),
		fastconf.WithProviderOrdered(p),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()

	revs := mgr.Snapshot().Cause().Revisions
	if got := revs[p.name]; got != p.rev {
		t.Fatalf("revision lost through WithProviderOrdered: got %q, want %q (revisions=%v)",
			got, p.rev, revs)
	}
}
