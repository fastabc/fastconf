package fastconf

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/internal/testutil"
)

type fingerprintConfig struct {
	Name string `json:"name"`
	Seq  int64  `json:"seq"`
}

var fpDecodeCalls atomic.Int64

type fpDecoded struct {
	Seq int64 `json:"seq"`
}

func (c *fpDecoded) UnmarshalJSON([]byte) error {
	c.Seq = fpDecodeCalls.Add(1)
	return nil
}

func TestInputFingerprint_NeverSkipsCustomDecode(t *testing.T) {
	type config struct {
		Value fpDecoded `json:"value"`
	}
	m, err := New[config](context.Background(), WithFS(fstest.MapFS{
		"conf.d/base/0.json": {Data: []byte(`{"value":{}}`)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Get().Value.Seq
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Value.Seq == before {
		t.Fatal("custom UnmarshalJSON was skipped for unchanged files")
	}
}

type replacingFingerprintCodec struct{ replacement bool }

func (c replacingFingerprintCodec) Decode([]byte) (map[string]any, error) {
	if !c.replacement {
		codec.Register("fingerprint-replacement", replacingFingerprintCodec{replacement: true})
	}
	return map[string]any{"replacement": c.replacement}, nil
}

func TestInputFingerprint_CodecChangesDuringAssembly(t *testing.T) {
	codec.RegisterExt("fpreplacement", "fingerprint-replacement")
	codec.Register("fingerprint-replacement", replacingFingerprintCodec{})
	m, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{
		"conf.d/base/0.fpreplacement": {Data: []byte("same input")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (*m.Get())["replacement"]; got != true {
		t.Fatalf("registered codec was skipped: replacement = %v", got)
	}
}

// mergeSpans counts fastconf.merge spans; a short-circuited reload runs no
// stage. A tracer is used rather than an observer because observers are
// owed StageFinished events and so disable the short-circuit.
func mergeSpans(tr *testutil.RecordingTracer) int {
	n := 0
	for _, sp := range tr.Spans() {
		if sp.Name == "fastconf.merge" {
			n++
		}
	}
	return n
}

func fpFS() fstest.MapFS {
	return fstest.MapFS{"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: a\n")}}
}

// TestInputFingerprint_SkipsUnchangedFileOnlyReload verifies: when
// every layer is a file whose bytes did not change and no user code runs in
// the pipeline, a reload skips merge through hash.
func TestInputFingerprint_SkipsUnchangedFileOnlyReload(t *testing.T) {
	tr := &testutil.RecordingTracer{}
	fs := fpFS()
	m, err := New[fingerprintConfig](context.Background(), WithFS(fs), WithTracer(tr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mergeSpans(tr); got != 1 {
		t.Fatalf("merge ran %d times; want 1 (the unchanged reload is short-circuited)", got)
	}
	fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte("name: b\n")}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mergeSpans(tr) != 2 || m.Get().Name != "b" {
		t.Fatalf("changed file must run the pipeline: merges=%d name=%q", mergeSpans(tr), m.Get().Name)
	}
}

// TestInputFingerprint_NeverSkipsExternalState: a transform that reads
// external state must run on every reload, even with identical files.
func TestInputFingerprint_NeverSkipsExternalState(t *testing.T) {
	var external atomic.Int64
	m, err := New[fingerprintConfig](context.Background(), WithFS(fpFS()), WithTransform(func(root map[string]any) error {
		root["seq"] = external.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	for want := int64(2); want <= 3; want++ {
		if err := m.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := m.Get().Seq; got != want {
			t.Fatalf("seq = %d; want %d — a transform reading external state was short-circuited", got, want)
		}
	}
}

type fpDefaulted struct {
	Name string `json:"name"`
	Seq  int64  `json:"seq"`
}

var fpDefaulterCalls atomic.Int64

func (c *fpDefaulted) Defaults() { c.Seq = fpDefaulterCalls.Add(1) }

// TestInputFingerprint_NeverSkipsDefaulter: a Defaulter may compute values
// from external state, so it disables the short-circuit.
func TestInputFingerprint_NeverSkipsDefaulter(t *testing.T) {
	m, err := New[fpDefaulted](context.Background(), WithFS(fpFS()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	before := m.Get().Seq
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Seq == before {
		t.Fatal("Defaulter did not run on reload: short-circuited")
	}
}

// TestInputFingerprint_RollbackInvalidates: after a rollback the live state
// no longer matches the last inputs, so an unchanged reload must run and
// republish the file state.
func TestInputFingerprint_RollbackInvalidates(t *testing.T) {
	fs := fpFS()
	m, err := New[fingerprintConfig](context.Background(), WithFS(fs), WithHistory(4))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte("name: b\n")}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.History().Rollback(m.History().List()[0]); err != nil {
		t.Fatal(err)
	}
	if m.Get().Name != "a" {
		t.Fatalf("rollback name = %q", m.Get().Name)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Name != "b" {
		t.Fatalf("reload after rollback stayed on %q; want the file state b", m.Get().Name)
	}
}

// TestInputFingerprint_OverrideNotSkipped: a one-shot override layer is not
// a file and always runs the pipeline.
func TestInputFingerprint_OverrideNotSkipped(t *testing.T) {
	m, err := New[fingerprintConfig](context.Background(), WithFS(fpFS()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background(), WithOverride(map[string]any{"name": "o"})); err != nil {
		t.Fatal(err)
	}
	if m.Get().Name != "o" {
		t.Fatalf("override name = %q", m.Get().Name)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Name != "a" {
		t.Fatalf("reload after override stayed on %q; want the file state a", m.Get().Name)
	}
}

// TestInputFingerprint_ObserversSeeStages: with an observer registered the
// reload runs (and reports) every stage even when inputs are unchanged.
func TestInputFingerprint_ObserversSeeStages(t *testing.T) {
	var merges atomic.Int64
	m, err := New[fingerprintConfig](context.Background(), WithFS(fpFS()), WithObserver(observerFunc(func(_ context.Context, e Event) {
		if s, ok := e.(StageFinished); ok && s.Stage == "merge" {
			merges.Add(1)
		}
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if merges.Load() != 2 {
		t.Fatalf("observer saw %d merge stages; want 2", merges.Load())
	}
}

// TestInputFingerprint_ProfileEnvChangeRuns: switching $APP_PROFILE selects
// different overlay files, so the fingerprint differs and the reload runs.
func TestInputFingerprint_ProfileEnvChangeRuns(t *testing.T) {
	t.Setenv("FP_PROFILE", "")
	fs := fstest.MapFS{
		"conf.d/base/00.yaml":          &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("name: prod\n")},
	}
	m, err := New[fingerprintConfig](context.Background(), WithFS(fs), WithProfile(Profile{Env: "FP_PROFILE"}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	t.Setenv("FP_PROFILE", "prod")
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Name != "prod" {
		t.Fatalf("name = %q after switching profile; want prod", m.Get().Name)
	}
}

// TestInputFingerprint_FailedReloadDoesNotPoison: a failed reload leaves the
// fingerprint of the published state, so restoring the file skips again and
// a fixed file publishes.
func TestInputFingerprint_FailedReloadDoesNotPoison(t *testing.T) {
	fs := fpFS()
	m, err := New[fingerprintConfig](context.Background(), WithFS(fs))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte("name: [\n")}
	if err := m.Reload(context.Background()); err == nil {
		t.Fatal("broken file should fail the reload")
	}
	fs["conf.d/base/00.yaml"] = &fstest.MapFile{Data: []byte("name: c\n")}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Get().Name != "c" {
		t.Fatalf("name = %q; want c", m.Get().Name)
	}
}
