package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
)

func runDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	pretty := fs.Bool("pretty", true, "indent JSON output")
	format := fs.String("format", "json", "output format: json | yaml")
	_ = fs.Parse(args)
	st, err := loadSnapshot(f)
	if err != nil {
		return err
	}
	if *format == "yaml" {
		dump := st.Dump
		if f.Unredacted {
			dump = st.Unredacted().Dump
		}
		b, err := dump(fastconf.YAML)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}
	view := st.Map
	if f.Unredacted {
		view = st.Unredacted().Map
	}
	m := view()
	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(m)
}
