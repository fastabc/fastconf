package fastconf

import (
	"context"
	"testing"

	"github.com/fastabc/fastconf/providers/source"
)

func TestProvenance_FullExplain(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("base", "yaml", []byte("name: from-base\ndb:\n  dsn: base-dsn\n  pool: 4\n"))),
		WithProvider(source.NewBytes("override", "yaml", []byte("name: from-override\ndb:\n  pool: 16\n"))),
		WithProvenance(ProvenanceFull),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	snap := mgr.Snapshot()
	chain := snap.Explain("name")
	if len(chain) < 2 {
		t.Fatalf("name chain=%d want >=2", len(chain))
	}
	winner := chain[len(chain)-1]
	if winner.Source.Path != "provider://override" {
		t.Fatalf("name winner=%s want provider://override", winner.Source.Path)
	}
	if got := snap.Explain("db.dsn"); len(got) != 1 || got[0].Source.Path != "provider://base" {
		t.Fatalf("db.dsn chain wrong: %+v", got)
	}
	got := snap.Explain("name")
	got[len(got)-1].Source.Path = "caller-mutation"
	if again := snap.Explain("name"); again[len(again)-1].Source.Path == "caller-mutation" {
		t.Fatal("Explain returned the snapshot's own provenance records")
	}
}

func TestProvenance_OffByDefault(t *testing.T) {
	mgr, err := New[snapshotConfig](context.Background(),
		WithFS(emptyFS()), WithProvider(source.NewBytes("a", "yaml", []byte("name: x\n"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()
	if got := mgr.Snapshot().Explain("name"); got != nil {
		t.Fatalf("expected no provenance by default, got %+v", got)
	}
}

func TestExplain_PerLayerValues(t *testing.T) {
	mgr, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "yaml", []byte("k: 1\n"))),
		WithProvider(source.NewBytes("over", "yaml", []byte("k: 2\n"))),
		WithProvenance(ProvenanceFull),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Close() }()

	chain := mgr.Snapshot().Explain("k")
	if len(chain) < 2 {
		t.Fatalf("want >=2 layers, got %d", len(chain))
	}
	last := chain[len(chain)-1]
	if v, ok := last.Value.(int); !ok || v != 2 {
		switch x := last.Value.(type) {
		case int:
			if x != 2 {
				t.Fatalf("winner value=%v", x)
			}
		case float64:
			if x != 2 {
				t.Fatalf("winner value=%v", x)
			}
		default:
			t.Fatalf("winner value type=%T value=%v", last.Value, last.Value)
		}
	}
}
