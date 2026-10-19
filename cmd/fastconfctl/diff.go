package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
)

func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f) // -dir / -profile / -provider available; -profile reused as "from"
	from := fs.String("from", "", "source profile (empty = base only)")
	to := fs.String("to", "", "target profile")
	jsonOut := fs.Bool("json", false, "output structured JSON diff instead of text")
	_ = fs.Parse(args)
	if *to == "" {
		return fmt.Errorf("--to is required")
	}
	fa, fb := f, f
	fa.Profile = *from
	fb.Profile = *to
	a, err := loadSnapshot(fa)
	if err != nil {
		return fmt.Errorf("load %q: %w", *from, err)
	}
	b, err := loadSnapshot(fb)
	if err != nil {
		return fmt.Errorf("load %q: %w", *to, err)
	}
	entries := a.Diff(b)
	if *jsonOut {
		return printJSONDiff(*from, *to, entries)
	}
	lines := fastconf.FormatDiff(entries)
	if len(lines) == 0 {
		fmt.Println("(no differences)")
		return nil
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

// printJSONDiff preserves the original CLI schema over State.Diff entries.
func printJSONDiff(from, to string, entries []fastconf.DiffEntry) error {
	var changes []map[string]any
	for _, e := range entries {
		c := map[string]any{"path": e.Path}
		switch e.Change {
		case fastconf.DiffAdded:
			c["op"], c["to"] = "+", e.After
		case fastconf.DiffRemoved:
			c["op"], c["from"] = "-", e.Before
		case fastconf.DiffModified:
			c["op"], c["from"], c["to"] = "~", e.Before, e.After
		}
		changes = append(changes, c)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"from": from, "to": to, "changes": changes})
}
