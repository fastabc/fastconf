package env_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/env"
)

func TestProvider_ProcessEnvironment(t *testing.T) {
	t.Setenv("FC_ENV_TEST_SERVER_PORT", "9090")
	t.Setenv("FC_ENV_TEST_ENABLED", "true")
	type config struct {
		Server struct {
			Port int `json:"port"`
		} `json:"server"`
		Enabled bool `json:"enabled"`
	}
	snapshot, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(env.New("FC_ENV_TEST_")), fastconf.WithUnknownFields(fastconf.UnknownError))
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Value(); got.Server.Port != 9090 || !got.Enabled {
		t.Fatalf("config = %+v", got)
	}
}
