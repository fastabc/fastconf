package fastconf_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf"
)

type sidecarExampleConfig struct {
	HTTP struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"http" json:"http"`
}

// Example_sidecar demonstrates a sidecar-style manager using the preset bundle.
func Example_sidecar() {
	root := mustExampleTempDir("example-sidecar-")
	defer os.RemoveAll(root)

	confDir := filepath.Join(root, "conf.d")
	configPath := filepath.Join(confDir, "base", "00-sidecar.yaml")
	mustWriteExampleFile(configPath, "http:\n  addr: \":8650\"\n")

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

	mustWriteExampleFile(configPath, "http:\n  addr: \":8651\"\n")
	if err := mgr.Reload(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	history := mgr.Replay().List()
	fmt.Printf("%s %d\n", mgr.Get().HTTP.Addr, len(history))
	// Output:
	// :8651 1
}
