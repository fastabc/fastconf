package fastconf

// White-box hot-path tests for assemble / canonicalHash / reload.
// These exercise branches that are unreachable through the public Reload() API
// without either cancellation racing or a failing Provider.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
)

// ---- TestAssemble_CtxCanceled_NoProviderCalled ---------------------------

// countingProvider wraps a static map and counts Load calls.
type countingProvider struct {
	loads atomic.Int64
	data  map[string]any
}

func (c *countingProvider) Name() string { return "counting" }
func (c *countingProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: 0}
}
func (c *countingProvider) Watch(_ context.Context, _ string) (<-chan contracts.Event, error) {
	return nil, nil
}
func (c *countingProvider) Load(_ context.Context) (contracts.Snapshot, error) {
	c.loads.Add(1)
	out := make(map[string]any, len(c.data))
	for k, v := range c.data {
		out[k] = v
	}
	return contracts.Snapshot{Map: out}, nil
}

// TestAssemble_CtxCanceled_NoProviderCalled verifies that assemble returns
// context.Canceled immediately without ever calling
// Provider.Load when the context is already done.
func TestAssemble_CtxCanceled_NoProviderCalled(t *testing.T) {
	cp := &countingProvider{data: map[string]any{"x": 1}}

	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("x: 1\n")},
	}
	m, err := New[map[string]any](context.Background(),
		func(o *options) { o.FS = mfs },
		func(o *options) { o.addProvider(cp) },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = m.Close() }()

	// Capture the load count after successful New (includes the initial reload).
	baseline := cp.loads.Load()

	// Cancel a fresh context BEFORE calling assemble.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = m.assemble(canceledCtx, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("assemble(canceledCtx) err = %v, want context.Canceled", err)
	}

	// Provider.Load must NOT have been called between the cancel and the assert.
	if delta := cp.loads.Load() - baseline; delta != 0 {
		t.Fatalf("Provider.Load called %d time(s) after context canceled; want 0", delta)
	}
}

// ---- TestCanonicalHashDeterministic -----------------------------

// The final value has a stable hash, and a different value changes it.
func TestCanonicalHashDeterministic(t *testing.T) {
	type cfg struct {
		Port int `json:"port"`
	}
	target := &cfg{Port: 8080}

	h1, err := canonicalHash(target)
	if err != nil {
		t.Fatalf("canonicalHash (1st call): %v", err)
	}
	h2, err := canonicalHash(target)
	if err != nil {
		t.Fatalf("canonicalHash (2nd call): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %x != %x", h1, h2)
	}
	// Sanity: a different value must produce a different hash.
	other := &cfg{Port: 9090}
	h3, err := canonicalHash(other)
	if err != nil {
		t.Fatalf("canonicalHash (3rd call): %v", err)
	}
	if h1 == h3 {
		t.Fatal("expected different hashes for different targets; got same")
	}
}

// ---- TestReloadWithKey_AssembleFailureSkipsCommit ------------------------

// errProvider always returns an error from Load so that assemble fails after
// the initial New succeeds (we add it via a second Reload, not at New time).
type errProvider struct {
	err error
}

func (e *errProvider) Name() string                     { return "err-provider" }
func (e *errProvider) Describe() contracts.ProviderInfo { return contracts.ProviderInfo{Priority: 0} }
func (e *errProvider) Watch(_ context.Context, _ string) (<-chan contracts.Event, error) {
	return nil, nil
}
func (e *errProvider) Load(_ context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{}, e.err
}

// TestReloadWithKey_AssembleFailureSkipsCommit verifies that when assemble
// returns an error:
//
//	(a) reload propagates the error,
//	(b) m.gen does NOT advance, and
//	(c) m.state still points at the prior snapshot.
func TestReloadWithKey_AssembleFailureSkipsCommit(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("port: 8080\n")},
	}
	m, err := New[map[string]any](context.Background(),
		func(o *options) { o.FS = mfs },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = m.Close() }()

	prevGen := m.gen.Load()
	prevState := m.state.Load()

	// Inject a failing provider so the NEXT assemble call fails.
	boom := errors.New("boom")
	m.opts.addProvider(&errProvider{err: boom})

	// The provider failure must abort assembly even with valid file layers.
	reloadErr := m.reload(context.Background(), "test-failure", "")
	if !errors.Is(reloadErr, boom) {
		t.Fatalf("reload error = %v, want provider cause %v", reloadErr, boom)
	}

	if m.gen.Load() != prevGen {
		t.Fatalf("gen advanced from %d to %d despite assemble failure", prevGen, m.gen.Load())
	}
	if m.state.Load() != prevState {
		t.Fatal("state pointer changed despite assemble failure")
	}
}
