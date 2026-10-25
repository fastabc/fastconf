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
	if *format == "yaml" {
		return dumpYAML(f)
	}
	st, err := loadSnapshot(f)
	if err != nil {
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

// dumpYAML loads a manager and writes the deterministic YAML form
// produced by State.Dump(YAML).
func dumpYAML(f cli.Flags) error {
	st, err := loadSnapshot(f)
	if err != nil {
		return err
	}
	dump := st.Dump
	if f.Unredacted {
		dump = st.Unredacted().Dump
	}
	b, err := dump(fastconf.YAML)
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(b)
	return nil
}
