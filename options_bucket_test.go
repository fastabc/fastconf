package fastconf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf/internal/coalesce"
	iopts "github.com/fastabc/fastconf/internal/options"
)

// TestBucketOptions_Profile_Combinations exercises the 5 ProfileOptions
// fields end-to-end through the bucketed WithProfile entry point. Each
// case independently asserts that the corresponding internal Options
// field is populated, so future refactors that split or merge fields
// will fail this test rather than silently regressing behaviour.
func TestBucketOptions_Profile_Combinations(t *testing.T) {
	t.Run("Single populates Profile", func(t *testing.T) {
		got := applyOpts(WithProfile(ProfileOptions{Single: "prod"}))
		if got.Profile != "prod" {
			t.Errorf("Single: Profile=%q want prod", got.Profile)
		}
	})
	t.Run("Multi populates Profiles", func(t *testing.T) {
		got := applyOpts(WithProfile(ProfileOptions{Multi: []string{"eu", "canary"}}))
		if len(got.Profiles) != 2 || got.Profiles[0] != "eu" || got.Profiles[1] != "canary" {
			t.Errorf("Multi: Profiles=%v want [eu canary]", got.Profiles)
		}
	})
	t.Run("Expr populates ProfileExpr", func(t *testing.T) {
		got := applyOpts(WithProfile(ProfileOptions{Expr: "prod & !canary"}))
		if got.ProfileExpr != "prod & !canary" {
			t.Errorf("Expr: ProfileExpr=%q", got.ProfileExpr)
		}
	})
	t.Run("EnvVar populates ProfileEnv", func(t *testing.T) {
		got := applyOpts(WithProfile(ProfileOptions{EnvVar: "MY_ENV"}))
		if got.ProfileEnv != "MY_ENV" {
			t.Errorf("EnvVar: ProfileEnv=%q want MY_ENV", got.ProfileEnv)
		}
	})
	t.Run("Default populates DefaultProf", func(t *testing.T) {
		got := applyOpts(WithProfile(ProfileOptions{Default: "dev"}))
		if got.DefaultProf != "dev" {
			t.Errorf("Default: DefaultProf=%q want dev", got.DefaultProf)
		}
	})
	t.Run("All-in-one composes without conflict", func(t *testing.T) {
		got := applyOpts(WithProfile(ProfileOptions{
			Single: "s", Multi: []string{"a"}, Expr: "x", EnvVar: "E", Default: "d",
		}))
		if got.Profile != "s" || got.Profiles[0] != "a" || got.ProfileExpr != "x" || got.ProfileEnv != "E" || got.DefaultProf != "d" {
			t.Errorf("all-in-one: %+v", got)
		}
	})
}

// TestBucketOptions_Watch_Combinations exercises the WatchOptions fields
// (Enabled, Paths, Coalesce, CoalesceProfile) and the override rule —
// per-field Coalesce wins over CoalesceProfile.
func TestBucketOptions_Watch_Combinations(t *testing.T) {
	t.Run("Enabled flips Watch flag", func(t *testing.T) {
		got := applyOpts(WithWatch(WatchOptions{Enabled: true}))
		if !got.Watch {
			t.Error("Watch flag not set")
		}
	})
	t.Run("Paths appends WatchPaths", func(t *testing.T) {
		got := applyOpts(WithWatch(WatchOptions{Paths: []string{"/etc/foo", "/etc/bar"}}))
		if len(got.WatchPaths) != 2 {
			t.Errorf("WatchPaths=%v", got.WatchPaths)
		}
	})
	t.Run("CoalesceProfile applies, per-field Coalesce overrides", func(t *testing.T) {
		got := applyOpts(WithWatch(WatchOptions{
			CoalesceProfile: coalesce.ProfileK8s,
			Coalesce:        CoalesceOptions{Quiet: 25 * time.Millisecond},
		}))
		if got.Coalesce.Quiet != 25*time.Millisecond {
			t.Errorf("per-field Quiet override lost: %v", got.Coalesce.Quiet)
		}
	})
	t.Run("Zero WatchOptions leaves Watch disabled", func(t *testing.T) {
		got := applyOpts(WithWatch(WatchOptions{}))
		if got.Watch {
			t.Error("zero WatchOptions must NOT enable watcher")
		}
	})
}

// TestBucketOptions_Coalesce_Combinations exercises the three timing
// knobs and verifies that omitted (zero) fields do NOT clobber an
// existing value populated by an earlier Option (typical Preset case).
func TestBucketOptions_Coalesce_Combinations(t *testing.T) {
	t.Run("Quiet alone is set", func(t *testing.T) {
		got := applyOpts(WithCoalesce(CoalesceOptions{Quiet: 33 * time.Millisecond}))
		if got.Coalesce.Quiet != 33*time.Millisecond {
			t.Errorf("Quiet=%v", got.Coalesce.Quiet)
		}
	})
	t.Run("Subsequent WithCoalesce preserves earlier non-zero fields", func(t *testing.T) {
		got := applyOpts(
			WithCoalesce(CoalesceOptions{Quiet: 10 * time.Millisecond, MaxLag: 500 * time.Millisecond}),
			WithCoalesce(CoalesceOptions{SwapHint: 5 * time.Millisecond}),
		)
		if got.Coalesce.Quiet != 10*time.Millisecond || got.Coalesce.MaxLag != 500*time.Millisecond || got.Coalesce.SwapHint != 5*time.Millisecond {
			t.Errorf("compose lost a field: %+v", got.Coalesce)
		}
	})
}

// TestBucketOptions_ProfileExprErrorMentionsBucketField proves the
// startup-time validator wraps invalid expressions with "WithProfile.Expr"
// (the SPEC-A1 / SPEC-F3 wording) so failures point users at the new
// bucketed API rather than the deleted scalar WithProfileExpr.
func TestBucketOptions_ProfileExprErrorMentionsBucketField(t *testing.T) {
	_, err := New[struct{}](context.Background(),
		WithFS(fstest.MapFS{
			"conf.d/base/00.yaml": &fstest.MapFile{Data: []byte("{}\n")},
		}),
		WithDir("conf.d"),
		WithProfile(ProfileOptions{Expr: "prod & ("}),
	)
	if err == nil {
		t.Fatal("expected expression error")
	}
	if !errors.Is(err, ErrDecode) {
		t.Errorf("want ErrDecode, got %v", err)
	}
	if !strings.Contains(err.Error(), "WithProfile.Expr") {
		t.Errorf("error must point at bucketed field: %v", err)
	}
}

// applyOpts collects the supplied Options against a fresh iopts.Options
// so individual fields can be asserted without running the full Manager
// pipeline.
func applyOpts(opts ...Option) *iopts.Options {
	o := &iopts.Options{}
	for _, fn := range opts {
		fn(o)
	}
	return o
}
