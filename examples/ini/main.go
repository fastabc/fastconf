// Command ini loads INI base/overlay files into a typed configuration.
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
)

//go:embed conf.d
var files embed.FS

type Config struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Server struct {
		Port    int           `json:"port" fc:"min=1,max=65535"`
		Enabled bool          `json:"enabled"`
		Timeout time.Duration `json:"timeout"`
	} `json:"server"`
}

func run(out io.Writer) error {
	// INI files use the same profile, merge and typed-validation pipeline.
	state, err := fastconf.Load[Config](context.Background(),
		fastconf.WithFS(files), fastconf.WithDir("conf.d"),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
		fastconf.WithStrictMerge(true), fastconf.WithUnknownFields(fastconf.UnknownError),
	)
	if err != nil {
		return err
	}

	// Inspect converted values, including booleans, durations and numeric strings.
	cfg := state.Value()
	_, err = fmt.Fprintf(out, "name=%s code=%s port=%d enabled=%t timeout=%s\n",
		cfg.Name, cfg.Code, cfg.Server.Port, cfg.Server.Enabled, cfg.Server.Timeout)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
