// Command fastconfctl is a CLI companion to FastConf for CI / ops:
//
//	fastconfctl dump      [-dir conf.d] [-profile prod]
//	fastconfctl diff      [-dir conf.d] -from dev -to prod
//	fastconfctl validate  [-dir conf.d] [-profile prod]
//	fastconfctl explain   [-dir conf.d] [-profile prod] <dotted.path>
//
// The tool reuses the public fastconf engine — no special access to
// internal packages — so its behaviour matches what the application
// would observe at runtime. The `explain` subcommand uses
// WithProvenance(ProvenanceFull) to print the per-path origin chain.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
	"github.com/fastabc/fastconf/pkg/mappath"
)

const usage = `fastconfctl <command> [flags]

Commands:
  dump      Print the merged configuration as JSON.
  diff      Diff merged configuration between two profiles.
  validate  Run the assemble+merge pipeline and report errors.
  explain   Show the origin chain for a dotted path.
  version   Print the binary version and exit.

Run 'fastconfctl <command> -h' for command-specific flags.`

// version is injected at build time via `-ldflags "-X main.version=<tag>"`
// by the dist pipeline. Default "dev" is reported when building from source
// without -ldflags (e.g. `go install`).
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "dump":
		err = runDump(args)
	case "diff":
		err = runDiff(args)
	case "validate":
		err = runValidate(args)
	case "explain":
		err = runExplain(args)
	case "version", "-v", "--version":
		fmt.Printf("fastconfctl %s\n", version)
		return
	case "-h", "--help", "help":
		fmt.Println(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s\n", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// loadDump constructs a manager from the supplied flags and returns the
// merged map (or empty map if the manager returned nil).
func loadDump(f cli.Flags) (map[string]any, error) {
	mgr, err := cli.LoadConfig[map[string]any](context.Background(), f)
	if err != nil {
		return nil, err
	}
	defer mgr.Close()
	v := mgr.Get()
	if v == nil {
		return map[string]any{}, nil
	}
	return *v, nil
}

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

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	_ = fs.Parse(args)
	if _, err := loadDump(f); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		return err
	}
	fmt.Println("OK")
	return nil
}

func runExplain(args []string) error {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("explain takes exactly one dotted path argument")
	}
	path := rest[0]
	mgr, err := cli.LoadConfig[map[string]any](context.Background(), f,
		fastconf.WithProvenance(fastconf.ProvenanceFull),
	)
	if err != nil {
		return err
	}
	defer mgr.Close()

	snap := mgr.Snapshot()
	v, ok := mappath.GetDotted(*snap.Value, path)
	if !ok {
		return fmt.Errorf("path %q not found", path)
	}
	chain := snap.Explain(path)
	origins := make([]map[string]any, 0, len(chain))
	for _, o := range chain {
		origins = append(origins, map[string]any{
			"path":     o.Source.Path,
			"kind":     o.Source.Kind.String(),
			"profile":  o.Source.Profile,
			"priority": o.Source.Priority,
			"codec":    o.Source.Codec,
			"value":    o.Value,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"path":    path,
		"value":   v,
		"origins": origins,
		"winner":  pickWinner(chain),
	})
}

func pickWinner(chain []fastconf.Origin) any {
	if len(chain) == 0 {
		return nil
	}
	w := chain[len(chain)-1]
	return map[string]any{
		"path":     w.Source.Path,
		"kind":     w.Source.Kind.String(),
		"priority": w.Source.Priority,
	}
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
