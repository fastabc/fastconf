package env

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

func TestProvider_Load(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		vars         []string
		configure    func(*Provider) *Provider
		want         map[string]any
	}{
		{"nested strings", "APP_", []string{"APP_DATABASE_POOL=20", "APP_ENABLED=true", "OTHER=x", "APP_=empty"}, nil, map[string]any{"database": map[string]any{"pool": "20"}, "enabled": "true"}},
		{"collapsed separators", "APP_", []string{"APP_DATABASE_DSN=first", "APP_DATABASE__DSN=last"}, nil, map[string]any{"database": map[string]any{"dsn": "last"}}},
		{"double underscore", "APP_", []string{"APP_DATABASE__POOL=20", "APP_FEATURE_FLAGS=on,off"}, func(p *Provider) *Provider { return p.WithReplacer(DoubleUnderscoreReplacer) }, map[string]any{"database": map[string]any{"pool": "20"}, "feature_flags": "on,off"}},
		{"coerce", "APP_", []string{"APP_PORT=20", "APP_ENABLED=true", "APP_RATE=1.5"}, func(p *Provider) *Provider { return p.WithCoerce(true) }, map[string]any{"port": int64(20), "enabled": true, "rate": 1.5}},
		{"no prefix", "", []string{"X=1"}, nil, map[string]any{"x": "1"}},
		{"namespace", "APP_", []string{"APP_DATABASE_DSN=postgres://x"}, func(p *Provider) *Provider { return p.At("config.runtime") }, map[string]any{"config": map[string]any{"runtime": map[string]any{"database": map[string]any{"dsn": "postgres://x"}}}}},
		{"custom replacer", "APP_", []string{"APP_SERVERxADDR=:8080"}, func(p *Provider) *Provider {
			return p.WithReplacer(EnvKeyReplacerFunc(func(key string) string { return strings.ToLower(strings.ReplaceAll(key, "x", ".")) }))
		}, map[string]any{"server": map[string]any{"addr": ":8080"}}},
		{"nil replacer restores default", "APP_", []string{"APP_SERVER_PORT=8080"}, func(p *Provider) *Provider { return p.WithReplacer(nil) }, map[string]any{"server": map[string]any{"port": "8080"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.prefix).withEnviron(func() []string { return tc.vars })
			if tc.configure != nil {
				p = tc.configure(p)
			}
			snapshot, err := p.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot.Map, tc.want) {
				t.Fatalf("Load().Map = %#v; want %#v", snapshot.Map, tc.want)
			}
			if p.Name() != "env:"+tc.prefix || p.Describe().Priority != contracts.PriorityEnv {
				t.Fatalf("metadata: name=%q info=%+v", p.Name(), p.Describe())
			}
		})
	}
}
