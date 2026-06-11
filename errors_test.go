package fastconf

import (
	"context"
	"errors"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/source"
)

func TestErrFastConfHierarchy(t *testing.T) {
	pe := &PolicyError{}
	if !errors.Is(pe, ErrPolicyDenied) {
		t.Fatal("PolicyError must satisfy Is(ErrPolicyDenied)")
	}
	if !errors.Is(pe, ErrFastConf) {
		t.Fatal("PolicyError must satisfy Is(ErrFastConf)")
	}
}

func TestAllSentinelsChainToErrFastConf(t *testing.T) {
	sentinels := map[string]error{
		"ErrNoSources":  ErrNoSources,
		"ErrValidation": ErrValidation,
		"ErrDecode":     ErrDecode,
		"ErrMerge":      ErrMerge,
		"ErrPatch":      ErrPatch,
		"ErrClosed":     ErrClosed,
		"ErrValidator":  ErrValidator,
		"ErrTransform":  ErrTransform,
		"ErrProvider":   ErrProvider,
		"ErrGenerator":  ErrGenerator,
		"ErrNoOrigin":   ErrNoOrigin,
	}
	for name, e := range sentinels {
		if !errors.Is(e, ErrFastConf) {
			t.Errorf("%s does not satisfy Is(ErrFastConf)", name)
		}
		if !errors.Is(e, e) {
			t.Errorf("%s does not satisfy Is(self)", name)
		}
	}
}

var errFailingProvider = errors.New("provider boom")

type failingProvider struct{}

func (failingProvider) Name() string  { return "failing" }
func (failingProvider) Priority() int { return 10 }
func (failingProvider) Load(context.Context) (map[string]any, error) {
	return nil, errFailingProvider
}
func (failingProvider) Watch(context.Context) (<-chan contracts.Event, error) {
	return nil, nil
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

func TestErrGeneratorClassification(t *testing.T) {
	_, err := New[map[string]any](context.Background(),
		WithFS(emptyFS()),
		WithGenerator(failingGenerator{}),
	)
	if !errors.Is(err, ErrGenerator) {
		t.Fatalf("want ErrGenerator, got %v", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("generator failure must not classify as ErrDecode: %v", err)
	}
}

func TestOrigin_LookupStrictNoOrigin(t *testing.T) {
	type cfg struct{}
	mgr, err := New[cfg](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("inline", "yaml", []byte("{}")), nil),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer mgr.Close()
	_, err = mgr.Snapshot().LookupStrict("does.not.exist")
	if !errors.Is(err, ErrNoOrigin) {
		t.Fatalf("expected ErrNoOrigin, got %v", err)
	}
}
