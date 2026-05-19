package sidecar_test

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/examples/internal/exutil"
)

type sidecarExampleConfig struct {
	HTTP struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"http" json:"http"`
}

// Example_sidecar demonstrates a sidecar-style manager using the preset bundle.
//
// See also: docs/cookbook/sidecar.md — the cmd/fastconfd companion
// daemon that wraps this same PresetSidecar configuration in an
// HTTP+SSE server.
func Example_sidecar() {
	root, cleanup := exutil.TempDir("example-sidecar-")
	defer cleanup()

	confDir := filepath.Join(root, "conf.d")
	configPath := filepath.Join(confDir, "base", "00-sidecar.yaml")
	exutil.WriteFile(configPath, "http:\n  addr: \":8650\"\n")

	mgr, err := fastconf.New[sidecarExampleConfig](context.Background(),
		fastconf.PresetSidecar(fastconf.SidecarOpts{
			Dir:      confDir,
			HistoryN: 2,
			Watch:    false,
			Strict:   true,
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	exutil.WriteFile(configPath, "http:\n  addr: \":8651\"\n")
	if err := mgr.Reload(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	history := mgr.Replay().List()
	fmt.Printf("%s %d\n", mgr.Get().HTTP.Addr, len(history))
	// Output:
	// :8651 1
}
