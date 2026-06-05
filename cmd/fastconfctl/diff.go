package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

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
	a, err := loadDump(fa)
	if err != nil {
		return fmt.Errorf("load %q: %w", *from, err)
	}
	b, err := loadDump(fb)
	if err != nil {
		return fmt.Errorf("load %q: %w", *to, err)
	}
	if *jsonOut {
		return printJSONDiff(*from, *to, a, b)
	}
	lines := diffMaps("", a, b)
	if len(lines) == 0 {
		fmt.Println("(no differences)")
		return nil
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return nil
}

// printJSONDiff emits a structured JSON diff suitable for machine consumption.
func printJSONDiff(from, to string, a, b map[string]any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"from":    from,
		"to":      to,
		"changes": buildJSONChanges("", a, b),
	})
}

// buildJSONChanges recursively builds a slice of structured change objects.
func buildJSONChanges(prefix string, a, b map[string]any) []map[string]any {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var out []map[string]any
	for _, k := range ordered {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		va, oka := a[k]
		vb, okb := b[k]
		switch {
		case oka && !okb:
			out = append(out, map[string]any{"op": "-", "path": full, "from": va})
		case !oka && okb:
			out = append(out, map[string]any{"op": "+", "path": full, "to": vb})
		default:
			ma, _ := va.(map[string]any)
			mb, _ := vb.(map[string]any)
			if ma != nil && mb != nil {
				out = append(out, buildJSONChanges(full, ma, mb)...)
				continue
			}
			if !valueEqual(va, vb) {
				out = append(out, map[string]any{"op": "~", "path": full, "from": va, "to": vb})
			}
		}
	}
	return out
}

// diffMaps returns a stable, line-oriented diff of two maps. "+" is
// added in b, "-" only in a, "~" changed.
func diffMaps(prefix string, a, b map[string]any) []string {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var out []string
	for _, k := range ordered {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		va, oka := a[k]
		vb, okb := b[k]
		switch {
		case oka && !okb:
			out = append(out, fmt.Sprintf("- %s = %v", full, va))
		case !oka && okb:
			out = append(out, fmt.Sprintf("+ %s = %v", full, vb))
		default:
			ma, _ := va.(map[string]any)
			mb, _ := vb.(map[string]any)
			if ma != nil && mb != nil {
				out = append(out, diffMaps(full, ma, mb)...)
				continue
			}
			if !valueEqual(va, vb) {
				out = append(out, fmt.Sprintf("~ %s : %v -> %v", full, va, vb))
			}
		}
	}
	return out
}

func valueEqual(a, b any) bool {
	pa, _ := json.Marshal(a)
	pb, _ := json.Marshal(b)
	return string(pa) == string(pb)
}
