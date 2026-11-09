package fastconf

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fastabc/fastconf/providers/source"
)

func TestInterfaceResolvedSecretsStayRedacted(t *testing.T) {
	for _, format := range []Format{JSON, YAML} {
		t.Run(format.String(), func(t *testing.T) {
			plain := "private-first"
			m, err := New[any](context.Background(), WithFS(emptyFS()),
				WithProvider(source.NewBytes("config", "json", []byte(`{"token":"ref:test","public":"visible"}`))),
				WithDecodeFormat(format), WithProvenance(ProvenanceFull), WithHistory(2),
				WithSecretResolvers(PrefixSecretResolver("test", "ref:", func(context.Context, string) (string, error) { return plain, nil })))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			check := func(s *State[any]) {
				t.Helper()
				if got := s.Map(); got["token"] != "***REDACTED***" || got["public"] != "visible" {
					t.Fatalf("unsafe map: %v", got)
				}
				for _, f := range []Format{JSON, YAML, TOML} {
					b, err := s.Dump(f)
					if err != nil || strings.Contains(string(b), "private-") {
						t.Fatalf("unsafe dump: %s (%v)", b, err)
					}
				}
				for _, o := range s.Explain("token") {
					if o.Value != nil && o.Value != "***REDACTED***" {
						t.Fatalf("unsafe origin: %+v", o)
					}
				}
			}
			before := m.Snapshot()
			check(before)
			if before.Unredacted().Map()["token"] != plain {
				t.Fatal("plaintext access changed")
			}
			plain = "private-second"
			preview, err := m.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			check(preview.Proposed)
			if err := m.Reload(context.Background()); err != nil {
				t.Fatal(err)
			}
			check(m.Snapshot())
			for _, diff := range [][]DiffEntry{preview.Diff, before.Diff(m.Snapshot())} {
				if len(diff) != 1 || diff[0].Before != "***REDACTED***" || diff[0].After != "***REDACTED***" {
					t.Fatalf("unsafe diff: %+v", diff)
				}
			}
			if err := m.History().Rollback(before); err != nil {
				t.Fatal(err)
			}
			check(m.Snapshot())
		})
	}
}

func TestRollbackRechecksRetentionOnWriter(t *testing.T) {
	m, err := New[snapshotConfig](context.Background(), WithFS(emptyFS()),
		WithProvider(source.NewBytes("base", "json", []byte(`{"name":"gamma"}`))), WithHistory(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "alpha"})); err != nil {
		t.Fatal(err)
	}
	target := m.Snapshot()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "beta"})); err != nil {
		t.Fatal(err)
	}
	before := m.Snapshot()
	callbacks := 0
	Subscribe(m, func(c *snapshotConfig) *string { return &c.Name }, func(_, _ *string) { callbacks++ })
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	m.reloadCh <- reloadRequest{run: func(context.Context) error { close(started); <-release; return nil }}
	<-started
	// The next queued reload evicts alpha before the rollback runs.
	reloaded := make(chan error, 1)
	m.reloadCh <- reloadRequest{reason: "manual", doneCh: reloaded}
	rolledBack := make(chan error, 1)
	go func() { rolledBack <- m.History().Rollback(target) }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for len(m.reloadCh) != 2 {
		select {
		case <-timer.C:
			t.Fatal("rollback was not queued")
		default:
			runtime.Gosched()
		}
	}
	close(release)
	if err := <-reloaded; err != nil {
		t.Fatal(err)
	}
	if err := <-rolledBack; !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("stale rollback = %v; want ErrUnknownGeneration", err)
	}
	if got := m.Snapshot(); got.Value().Name != "gamma" || got.Generation() != before.Generation()+1 {
		t.Fatalf("stale rollback changed state: %+v", got)
	}
	if got := m.History().List(); len(got) != 1 || got[0] != before {
		t.Fatal("stale rollback changed history")
	}
	if callbacks != 1 {
		t.Fatalf("callbacks = %d; only the successful reload should notify", callbacks)
	}
}
