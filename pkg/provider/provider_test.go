package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

// Default DotReplacer (single "_" → "."): values stay as strings; the
// typed-decode chain (see pkg/decoder.StringPrimitiveHook) converts
// them to the destination field type at *T decode time. Consecutive
// underscore runs collapse to a single dot so DATABASE__POOL still
// produces "database.pool" rather than "database..pool".
func TestEnvProvider_DotReplacerDefault(t *testing.T) {
	p := NewEnv("APP_").withEnviron(func() []string {
		return []string{
			"APP_DATABASE_POOL=20",
			"APP_DATABASE_DSN=postgres://x",
			"APP_FEATURES_ENABLED=true",
			"APP_RATE=1.5",
			"APP_DATABASE__DSN=postgres://collapsed", // overrides above; runs collapse
			"NOT_RELATED=ignored",
			"APP_=skip-empty",
		}
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"database": map[string]any{
			"pool": "20",
			"dsn":  "postgres://collapsed",
		},
		"features": map[string]any{
			"enabled": "true",
		},
		"rate": "1.5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

// DoubleUnderscoreReplacer preserves single "_" as part of the key.
func TestEnvProvider_DoubleUnderscoreReplacer(t *testing.T) {
	p := NewEnv("APP_").WithReplacer(DoubleUnderscoreReplacer).withEnviron(func() []string {
		return []string{
			"APP_DATABASE__POOL=20",
			"APP_FEATURE_FLAGS=on,off", // single "_" preserved
		}
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"database":      map[string]any{"pool": "20"},
		"feature_flags": "on,off",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

// Legacy bool/int/float coercion is opt-in via WithCoerce(true).
func TestEnvProvider_WithCoerceTrue(t *testing.T) {
	p := NewEnv("APP_").WithCoerce(true).withEnviron(func() []string {
		return []string{
			"APP_DATABASE_POOL=20",
			"APP_FEATURES_ENABLED=true",
			"APP_RATE=1.5",
		}
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"database": map[string]any{"pool": int64(20)},
		"features": map[string]any{"enabled": true},
		"rate":     1.5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestEnvProvider_NoPrefix(t *testing.T) {
	p := NewEnv("").withEnviron(func() []string { return []string{"X=1"} })
	got, _ := p.Load(context.Background())
	if got["x"] != "1" {
		t.Errorf("got %#v", got)
	}
}

// At() grafts the loaded tree under a configurable root path.
func TestEnvProvider_AtNamespaces(t *testing.T) {
	p := NewEnv("APP_").At("config.runtime").withEnviron(func() []string {
		return []string{"APP_DATABASE_DSN=postgres://x"}
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"config": map[string]any{
			"runtime": map[string]any{
				"database": map[string]any{"dsn": "postgres://x"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestCLIProvider(t *testing.T) {
	p := NewCLI(map[string]any{"server": map[string]any{"addr": ":9090"}})
	got, _ := p.Load(context.Background())
	if got["server"].(map[string]any)["addr"] != ":9090" {
		t.Error("cli load mismatch")
	}
	if p.Priority() != contracts.PriorityCLI {
		t.Error("priority")
	}
}

func TestCLIProvider_ChangedAliasMatchesNewCLI(t *testing.T) {
	want := map[string]any{"server": map[string]any{"addr": ":9090"}}
	p := NewCLIChanged(want)
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
	if p.Priority() != contracts.PriorityCLI {
		t.Error("priority")
	}
}
