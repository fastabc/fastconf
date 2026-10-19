package fastconf

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/policy"
)

func TestErrFastConfHierarchy(t *testing.T) {
	pe := &PolicyError{}
	if !errors.Is(pe, ErrInvalid) {
		t.Fatal("PolicyError must satisfy Is(ErrInvalid)")
	}
	if !errors.Is(pe, ErrFastConf) {
		t.Fatal("PolicyError must satisfy Is(ErrFastConf)")
	}
}

// TestCoreSentinelsChainToErrFastConf verifies the error table: eight
// reload sentinels plus the two History misuse errors, all under the
// umbrella.
func TestCoreSentinelsChainToErrFastConf(t *testing.T) {
	sentinels := map[string]error{
		"ErrNoSources": ErrNoSources,
		"ErrDecode":    ErrDecode,
		"ErrMerge":     ErrMerge,
		"ErrTransform": ErrTransform,
		"ErrProvider":  ErrProvider,
		"ErrInvalid":   ErrInvalid,
		"ErrClosed":    ErrClosed,
		"ErrTooLarge":  ErrTooLarge,
		// History errors use the same umbrella sentinel.
		"ErrHistoryDisabled":   ErrHistoryDisabled,
		"ErrUnknownGeneration": ErrUnknownGeneration,
	}
	for name, e := range sentinels {
		if !errors.Is(e, ErrFastConf) {
			t.Errorf("%s does not satisfy Is(ErrFastConf)", name)
		}
	}
	if contracts.ErrTooLarge != ErrTooLarge {
		t.Error("contracts.ErrTooLarge must be ErrTooLarge")
	}
}

func TestErrMergeCoversPatchFailure(t *testing.T) {
	_, err := New[map[string]any](context.Background(), WithFS(fstest.MapFS{
		"conf.d/base/00.yaml":       &fstest.MapFile{Data: []byte("a: 1\n")},
		"conf.d/base/10.patch.json": &fstest.MapFile{Data: []byte(`[{"op":"remove","path":"/missing"}]`)},
	}))
	if !errors.Is(err, ErrMerge) {
		t.Fatalf("want ErrMerge for a failing patch, got %v", err)
	}
}

var errFailingProvider = errors.New("provider boom")

type failingProvider struct{}

func (failingProvider) Name() string { return "failing" }

func (failingProvider) Load(context.Context) (contracts.Snapshot, error) {
	return contracts.Snapshot{}, errFailingProvider
}

func TestErrProviderClassification(t *testing.T) {
	_, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithProvider(failingProvider{}),
	)
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("want ErrProvider, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("provider load failure must not classify as ErrDecode: %v", err)
	}
}

var errFailingGenerator = errors.New("generator boom")

type failingGenerator struct{}

func (failingGenerator) Name() string { return "failing" }

func (failingGenerator) Generate(context.Context) ([]contracts.RawLayer, error) {
	return nil, errFailingGenerator
}

func TestGeneratorFailureIsErrProvider(t *testing.T) {
	_, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithGenerator(failingGenerator{}),
	)
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("want ErrProvider for a generator failure, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("generator failure must not classify as ErrDecode: %v", err)
	}
}

func TestPolicyErrorMessage(t *testing.T) {
	err := &PolicyError{Violations: []policy.Violation{{Rule: "r1", Path: "a", Message: "m1"}, {Rule: "r2", Path: "b", Message: "m2"}}}
	if got, want := err.Error(), "fastconf: policy denied: r1@a: m1; r2@b: m2"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
