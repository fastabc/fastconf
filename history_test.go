package fastconf

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"

	istate "github.com/fastabc/fastconf/internal/state"
	"github.com/fastabc/fastconf/pkg/source"
)

type phase7Cfg struct {
	Name string `json:"name" yaml:"name"`
	DB   struct {
		DSN  string `json:"dsn" yaml:"dsn"`
		Pool int    `json:"pool" yaml:"pool"`
	} `json:"db" yaml:"db"`
}

// emptyFS provides an empty conf.d so the file-discovery layer produces no
// layers, leaving WithBytes as the only contributor.
func emptyFS() fstest.MapFS {
	return fstest.MapFS{
		"conf.d/base/.keep": &fstest.MapFile{Data: []byte{}},
	}
}

func TestProvenance_FullExplain(t *testing.T) {
	mgr, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("base", "yaml", []byte("name: from-base\ndb:\n  dsn: base-dsn\n  pool: 4\n")), nil),
		WithSource(source.NewBytes("override", "yaml", []byte("name: from-override\ndb:\n  pool: 16\n")), nil),
		WithProvenance(ProvenanceFull),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	snap := mgr.Snapshot()
	if snap.Origins() == nil {
		t.Fatal("expected origin index")
	}
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
}

func TestProvenance_OffByDefault(t *testing.T) {
	mgr, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("a", "yaml", []byte("name: x\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if mgr.Snapshot().Origins() != nil {
		t.Fatal("expected nil origins by default")
	}
}

func TestHistory_RingAndRollback(t *testing.T) {
	mgr, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("a", "yaml", []byte("name: gen1\n")), nil),
		WithHistory(3),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if hist := mgr.Replay().List(); len(hist) != 0 {
		t.Fatalf("history=%d want 0 (no prior commit)", len(hist))
	}

	// Rollback to a fake snapshot with a generation that doesn't exist.
	fake := wrapState(istate.NewSnapshot[phase7Cfg](nil, [32]byte{}, 0, nil, 999, nil, istate.ReloadCause{}, nil, nil))
	if err := mgr.Replay().Rollback(fake); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("rollback unknown gen err=%v", err)
	}
	if err := mgr.Replay().Rollback(nil); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("rollback nil err=%v want ErrUnknownGeneration", err)
	}
}

func TestHistory_Disabled(t *testing.T) {
	mgr, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("a", "yaml", []byte("name: x\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if hist := mgr.Replay().List(); hist != nil {
		t.Fatalf("Replay().List() = %v; want nil when history disabled", hist)
	}
	fake := wrapState(istate.NewSnapshot[phase7Cfg](nil, [32]byte{}, 0, nil, 1, nil, istate.ReloadCause{}, nil, nil))
	if err := mgr.Replay().Rollback(fake); !errors.Is(err, ErrHistoryDisabled) {
		t.Fatalf("Rollback() err=%v want ErrHistoryDisabled", err)
	}
}

func TestPauseResumeWatch(t *testing.T) {
	mgr, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("a", "yaml", []byte("name: x\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if mgr.Watcher().Paused() {
		t.Fatal("default should not be paused")
	}
	mgr.Watcher().Pause()
	if !mgr.Watcher().Paused() {
		t.Fatal("expected paused")
	}
	mgr.Watcher().Resume()
	if mgr.Watcher().Paused() {
		t.Fatal("expected resumed")
	}
}

func TestState_Diff(t *testing.T) {
	a, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("a", "yaml", []byte("name: alpha\ndb:\n  dsn: x\n  pool: 5\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New[phase7Cfg](context.Background(),
		WithFS(emptyFS()), WithSource(source.NewBytes("b", "yaml", []byte("name: beta\ndb:\n  dsn: x\n  pool: 9\n")), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	out := a.Snapshot().Diff(b.Snapshot())
	if len(out) == 0 {
		t.Fatal("expected diffs")
	}
	var nameSeen, poolSeen bool
	for _, e := range out {
		if e.Change != DiffModified {
			continue
		}
		switch e.Path {
		case "name":
			if fmt.Sprintf("%v -> %v", e.Before, e.After) == "alpha -> beta" {
				nameSeen = true
			}
		case "db.pool":
			// JSON round-trip turns numeric scalars into float64; compare
			// via Sprintf to keep the assertion typed-display-agnostic.
			if fmt.Sprintf("%v -> %v", e.Before, e.After) == "5 -> 9" {
				poolSeen = true
			}
		}
	}
	if !nameSeen || !poolSeen {
		t.Fatalf("missing diffs: %+v", out)
	}
	// FormatDiff round-trips the structured entries back to the legacy
	// human-readable lines so existing log scrapers keep working.
	lines := FormatDiff(out)
	if len(lines) != len(out) {
		t.Errorf("FormatDiff lost entries: %d → %d", len(out), len(lines))
	}
}

func TestLookup_PerLayerValues(t *testing.T) {
	mgr, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("base", "yaml", []byte("k: 1\n")), nil),
		WithSource(source.NewBytes("over", "yaml", []byte("k: 2\n")), nil),
		WithProvenance(ProvenanceFull),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	chain := mgr.Snapshot().Lookup("k")
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
