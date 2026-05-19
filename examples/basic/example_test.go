package basic_test

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/examples/internal/exutil"
)

type basicExampleConfig struct {
	Server struct {
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`
	Database struct {
		Pool int `yaml:"pool" json:"pool"`
	} `yaml:"database" json:"database"`
}

// Example_basic demonstrates loading a profile overlay from a config directory.
//
// See also: docs/cookbook/k8s.md — profile-aware overlay loading with
// fastconf.WithProfile under a typical Kubernetes layout.
func Example_basic() {
	root, cleanup := exutil.TempDir("example-basic-")
	defer cleanup()

	confDir := filepath.Join(root, "conf.d")
	exutil.WriteFile(filepath.Join(confDir, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\ndatabase:\n  pool: 10\n")
	exutil.WriteFile(filepath.Join(confDir, "overlays", "prod", "10-app.yaml"), "server:\n  addr: \":8443\"\ndatabase:\n  pool: 32\n")

	restoreEnv := exutil.SetEnv("APP_PROFILE", "prod")
	defer restoreEnv()

	mgr, err := fastconf.New[basicExampleConfig](context.Background(),
		fastconf.WithDir(confDir),
		fastconf.WithProfile(fastconf.ProfileOptions{
			EnvVar:  "APP_PROFILE",
			Default: "dev",
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	app := mgr.Get()
	fmt.Printf("%s %d %d\n", app.Server.Addr, app.Database.Pool, len(mgr.Snapshot().Sources))
	// Output:
	// :8443 32 2
}
