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

// TestCoreSentinelsChainToErrFastConf verifies reload, notification and history
// errors share the umbrella sentinel.
func TestCoreSentinelsChainToErrFastConf(t *testing.T) {
	sentinels := map[string]error{
		"ErrNoSources": ErrNoSources,
		"ErrDecode":    ErrDecode,
		"ErrMerge":     ErrMerge,
		"ErrTransform": ErrTransform,
		"ErrProvider":  ErrProvider,
		"ErrInvalid":   ErrInvalid,
		"ErrClosed":    ErrClosed,
		"ErrPaused":    ErrPaused,
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

var errFailingGenerator = errors.New("generator boom")

type failingGenerator struct{}

func (failingGenerator) Name() string { return "failing" }

func (failingGenerator) Generate(context.Context) ([]contracts.RawLayer, error) {
	return nil, errFailingGenerator
}

func TestSourceFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opt   Option
		cause error
	}{
		{"provider", WithProvider(failingProvider{}), errFailingProvider},
		{"generator", WithGenerator(failingGenerator{}), errFailingGenerator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New[map[string]any](context.Background(), WithFS(emptyFS()), tc.opt)
			if !errors.Is(err, ErrProvider) || !errors.Is(err, tc.cause) || errors.Is(err, ErrDecode) {
				t.Fatalf("New = %v; want ErrProvider wrapping %v and no ErrDecode", err, tc.cause)
			}
		})
	}
}

func TestPolicyErrorMessage(t *testing.T) {
	err := &PolicyError{Violations: []policy.Violation{{Rule: "r1", Path: "a", Message: "m1"}, {Rule: "r2", Path: "b", Message: "m2"}}}
	if got, want := err.Error(), "fastconf: policy denied: r1@a: m1; r2@b: m2"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
