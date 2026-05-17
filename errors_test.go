package fastconf

import (
	"context"
	"errors"
	"testing"

	"github.com/fastabc/fastconf/pkg/source"
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
