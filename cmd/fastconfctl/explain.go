package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
	mappath "github.com/fastabc/fastconf/confmap"
)

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
	if snap.Value() == nil {
		return fmt.Errorf("snapshot value is nil")
	}
	v, ok := mappath.GetDotted(*snap.Value(), path)
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
