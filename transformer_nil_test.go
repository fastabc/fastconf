package fastconf_test

// WithTransformers must reject nil entries at construction. runTransform
// invokes Name()/Transform() on the reload goroutine with no recover, so a
// nil transformer would panic there and breach the fail-safe contract.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

func TestWithTransformersNilIsDeferredError(t *testing.T) {
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	type cfg struct {
		Name string `json:"name"`
	}
	_, err := fastconf.New[cfg](context.Background(),
		fastconf.WithFS(mfs),
		fastconf.WithDir("conf.d"),
		fastconf.WithTransformers(nil),
	)
	if err == nil {
		t.Fatal("expected nil transformer to surface as deferred error")
	}
	if !errors.Is(err, fastconf.ErrFastConf) {
		t.Errorf("error %q does not chain to ErrFastConf", err)
	}
	if !strings.Contains(err.Error(), "WithTransformers") {
		t.Errorf("error %q does not mention WithTransformers", err)
	}
}
