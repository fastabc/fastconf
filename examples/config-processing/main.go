// Command config-processing demonstrates defaults, interpolation and typed KV inputs.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
	"github.com/fastabc/fastconf/transform"
)

//go:embed config.yaml
var document []byte

type Level int
type Flag bool

type Config struct {
	Enabled bool          `json:"enabled" default:"true"`
	Retries int           `json:"retries" default:"5"`
	Timeout time.Duration `json:"timeout" default:"5s"`
	Level   Level         `json:"level"`
	Feature Flag          `json:"feature"`
	Hosts   []string      `json:"hosts"`
	Ports   []int         `json:"ports"`
	Regions []string      `json:"regions"`
	Server  struct {
		URL string `json:"url"`
	} `json:"server"`
	Callback string `json:"callback"`
	Owner    string `json:"owner"`
	Literal  string `json:"literal"`
}

func load(lookup func(string) (string, bool)) (*fastconf.State[Config], error) {
	return fastconf.Load[Config](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("settings", "yaml", document)),
		fastconf.WithTransform(transform.Interpolate(transform.InterpolateOptions{Lookup: lookup, Refs: true})),
		fastconf.WithProvenance(fastconf.ProvenanceFull),
		fastconf.WithUnknownFields(fastconf.UnknownError))
}

func run(out io.Writer) error {
	// Deterministic demo lookup; omit Lookup to use os.LookupEnv in production.
	state, err := load(func(name string) (string, bool) {
		if name == "OWNER" {
			return "demo", true
		}
		return "", false
	})
	if err != nil {
		return err
	}
	cfg := state.Value()
	origins := state.Explain("timeout")
	fromDefault := len(origins) > 0 && origins[len(origins)-1].Source.Kind == fastconf.LayerDefault
	_, err = fmt.Fprintf(out, "enabled=%t retries=%d timeout=%s default-source=%t\nlevel=%d feature=%t hosts=%v ports=%v regions=%v\ncallback=%s owner=%s literal=%s\n",
		cfg.Enabled, cfg.Retries, cfg.Timeout, fromDefault, cfg.Level, cfg.Feature, cfg.Hosts, cfg.Ports, cfg.Regions, cfg.Callback, cfg.Owner, cfg.Literal)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
