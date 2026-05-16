package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

func TestEnvProvider_NestedAndCoercion(t *testing.T) {
	p := NewEnv("APP_").withEnviron(func() []string {
		return []string{
			"APP_DATABASE__POOL=20",
			"APP_DATABASE__DSN=postgres://x",
			"APP_FEATURES__ENABLED=true",
			"APP_RATE=1.5",
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
			"pool": int64(20),
			"dsn":  "postgres://x",
		},
		"features": map[string]any{
			"enabled": true,
		},
		"rate": 1.5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestEnvProvider_NoPrefix(t *testing.T) {
	p := NewEnv("").withEnviron(func() []string { return []string{"X=1"} })
	got, _ := p.Load(context.Background())
	if got["x"] != int64(1) {
		t.Errorf("got %#v", got)
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
