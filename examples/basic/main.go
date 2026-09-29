// Command basic loads a typed configuration from base, a profile, and environment.
package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/env"
)

//go:embed conf.d
var files embed.FS

type Config struct {
	Server struct {
		Port    int           `json:"port" fc:"min=1,max=65535"`
		Timeout time.Duration `json:"timeout"`
	} `json:"server"`
	Database struct {
		Pool int `json:"pool" fc:"min=1"`
	} `json:"database"`
}

func run(out io.Writer) error {
	state, err := fastconf.Load[Config](context.Background(),
		fastconf.WithFS(files),
		fastconf.WithDir("conf.d"),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
		fastconf.WithProvider(env.New("FC_EXAMPLE_")),
		fastconf.WithUnknownFields(fastconf.UnknownError),
	)
	if err != nil {
		return err
	}
	cfg := state.Value()
	fmt.Fprintf(out, "port=%d timeout=%s pool=%d\n", cfg.Server.Port, cfg.Server.Timeout, cfg.Database.Pool)
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
