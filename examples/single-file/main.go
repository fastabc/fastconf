// Command single-file loads one YAML document at startup.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/fastabc/fastconf"
)

type Config struct {
	Port int `json:"port" yaml:"port" fc:"min=1,max=65535"`
}

func run(path string, out io.Writer) error {
	cfg, err := fastconf.LoadFile[Config](path)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "port=%d\n", cfg.Port)
	return err
}

func main() {
	path := flag.String("file", "examples/single-file/app.yaml", "configuration file")
	flag.Parse()
	if err := run(*path, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
