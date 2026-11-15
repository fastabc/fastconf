//go:build ignore

// Command pflag layers explicitly changed CLI flags over a document.
package main

import (
	"context"
	"fmt"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	adapter "github.com/fastabc/fastconf/integrations/cli/pflag"
	"github.com/fastabc/fastconf/providers/cliflag"
	"github.com/fastabc/fastconf/providers/source"
	"github.com/spf13/pflag"
)

type Config struct {
	Server struct {
		Port int    `json:"port" fc:"min=1,max=65535"`
		Host string `json:"host" fc:"required"`
	} `json:"server"`
	Debug bool `json:"debug" default:"true"`
}

func main() {
	flags := pflag.NewFlagSet("example", pflag.ContinueOnError)
	flags.Int("server.port", 8080, "server port")
	flags.String("server.host", "localhost", "server host")
	flags.Bool("debug", true, "enable debug")
	// Deterministic demo arguments; use os.Args[1:] in an application.
	if err := flags.Parse([]string{"--server.port=9090", "--debug=false"}); err != nil {
		panic(err)
	}
	changed := adapter.FromChanged(flags)
	state, err := fastconf.Load[Config](context.Background(),
		fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(
			source.NewBytes("base", "yaml", []byte("server: {host: file-host, port: 8000}\ndebug: true\n")),
			cliflag.New(changed)),
		fastconf.WithUnknownFields(fastconf.UnknownError))
	if err != nil {
		panic(err)
	}
	cfg := state.Value()
	fmt.Printf("host=%s port=%d debug=%t\n", cfg.Server.Host, cfg.Server.Port, cfg.Debug)
}
