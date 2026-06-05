package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	m, err := loadDump(f)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(m)
}

// dumpYAML loads a manager and writes the deterministic YAML form
// produced by State.Dump(DumpYAML, nil).
func dumpYAML(f cli.Flags) error {
	mgr, err := cli.LoadConfig[map[string]any](context.Background(), f)
	if err != nil {
		return err
	}
	defer mgr.Close()
	st := mgr.Snapshot()
	if st == nil {
		return fmt.Errorf("no state available")
	}
	b, err := st.Dump(fastconf.DumpYAML, nil)
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(b)
	return nil
}
