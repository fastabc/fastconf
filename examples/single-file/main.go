// Command single-file loads one YAML document at startup.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
)

type Config struct {
	Port int `json:"port" yaml:"port" fc:"min=1,max=65535"`
}

func run(ctx context.Context, path string, out io.Writer) error {
	state, err := fastconf.Load[Config](ctx,
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithUnknownFields(fastconf.UnknownError),
	)
	if err != nil {
		return err
	}
	cfg := state.Value()
	_, err = fmt.Fprintf(out, "port=%d\n", cfg.Port)
	return err
}

func main() {
	path := flag.String("file", "examples/single-file/app.yaml", "configuration file")
	flag.Parse()
	if err := run(context.Background(), *path, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
