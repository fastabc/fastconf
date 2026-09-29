// Command features evaluates file-backed targeting and deterministic rollouts.
package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/feature"
)

//go:embed conf.d
var files embed.FS

type Config struct {
	Features map[string]feature.Rule `json:"features"`
}

func run(out io.Writer) error {
	state, err := fastconf.Load[Config](context.Background(),
		fastconf.WithFS(files),
		fastconf.WithDir("conf.d"),
		fastconf.WithProfile(fastconf.Profile{Names: []string{"demo"}}),
	)
	if err != nil {
		return err
	}
	rules := state.Value().Features
	for _, user := range []struct{ id, tier string }{
		{"alice", "enterprise"},
		{"bob", "starter"},
		{"carol", "starter"},
	} {
		attrs := feature.EvalContext{"user.id": user.id, "tier": user.tier}
		value := feature.Eval(rules, "newCheckout", attrs, false)
		fmt.Fprintf(out, "%s checkout=%v stable=%t\n", user.id, value,
			value == feature.Eval(rules, "newCheckout", attrs, false))
	}
	fmt.Fprintf(out, "missing flag=%v missing anchor=%v\n",
		feature.Eval(rules, "missing", nil, false),
		feature.Eval(rules, "newCheckout", nil, false))
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
