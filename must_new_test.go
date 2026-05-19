package fastconf_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

// TestMustNew_PanicOnError pins SPEC-A9: when the initial reload would
// have returned an error, MustNew panics with a wrapped error whose
// message references the helper.
func TestMustNew_PanicOnError(t *testing.T) {
	t.Run("happy path returns manager", func(t *testing.T) {
		mfs := fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("addr: x\n")},
		}
		mgr := fastconf.MustNew[map[string]any](context.Background(),
			fastconf.WithFS(mfs),
			fastconf.WithDir("conf.d"),
		)
		defer mgr.Close()
		if mgr.Get() == nil {
			t.Fatal("MustNew returned manager but Get() is nil")
		}
	})

	t.Run("panics with wrapped error", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("MustNew did not panic on bad options")
			}
			err, ok := r.(error)
			if !ok {
				t.Fatalf("panic payload type = %T, want error", r)
			}
			if !strings.Contains(err.Error(), "fastconf.MustNew") {
				t.Errorf("panic message missing helper prefix: %v", err)
			}
		}()
		// WithLogger(nil) is recorded as a deferred construction error;
		// it causes New (and thus MustNew) to fail before any state is
		// allocated.
		_ = fastconf.MustNew[map[string]any](context.Background(),
			fastconf.WithLogger(nil),
		)
	})
}
