package fastconf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf/pkg/source"
)

func TestProfile_ProfilesMatchExpression(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":                &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod-eu/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod & eu\n")},
		"conf.d/overlays/prod-eu/00.yaml":    &fstest.MapFile{Data: []byte("name: prod-eu\n")},
		"conf.d/overlays/prod-us/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod & us\n")},
		"conf.d/overlays/prod-us/00.yaml":    &fstest.MapFile{Data: []byte("name: prod-us\n")},
		"conf.d/overlays/canary/_meta.yaml":  &fstest.MapFile{Data: []byte("match: canary\n")},
		"conf.d/overlays/canary/00.yaml":     &fstest.MapFile{Data: []byte("name: canary\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfiles("prod", "eu"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Name; got != "prod-eu" {
		t.Fatalf("expected prod-eu overlay to win, got %q", got)
	}
}

func TestProfile_FallbackToNameMembership(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":          &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("name: prod\n")},
		"conf.d/overlays/dev/00.yaml":  &fstest.MapFile{Data: []byte("name: dev\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfiles("prod"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("name-membership fallback failed, got %q", got)
	}
}

func TestProfile_GlobalProfileExpr(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":               &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml":      &fstest.MapFile{Data: []byte("name: prod\n")},
		"conf.d/overlays/canary/_meta.yaml": &fstest.MapFile{Data: []byte("match: prod\n")},
		"conf.d/overlays/canary/00.yaml":    &fstest.MapFile{Data: []byte("name: canary\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfiles("prod"),
		WithProfileExpr("!canary"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("global expr should suppress canary, got %q", got)
	}
}

func TestProfile_LegacySingleProfile(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	mfs := fstest.MapFS{
		"conf.d/base/00.yaml":          &fstest.MapFile{Data: []byte("name: base\n")},
		"conf.d/overlays/prod/00.yaml": &fstest.MapFile{Data: []byte("name: prod\n")},
	}
	mgr, err := New[cfg](context.Background(),
		WithFS(mfs), WithDir("conf.d"),
		WithProfile("prod"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer mgr.Close()
	if got := mgr.Get().Name; got != "prod" {
		t.Fatalf("legacy WithProfile broken, got %q", got)
	}
}

func TestProfile_InvalidExprFailsAtNew(t *testing.T) {
	_, err := New[struct{}](context.Background(),
		WithFS(emptyFS()),
		WithSource(source.NewBytes("inline", "yaml", []byte("{}")), nil),
		WithProfileExpr("prod & ("),
	)
	if err == nil || !strings.Contains(err.Error(), "WithProfileExpr") {
		t.Fatalf("expected WithProfileExpr decode error, got %v", err)
	}
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("expected ErrDecode, got %v", err)
	}
}
