package fastconf_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf"
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
func Example_basic() {
	root := mustExampleTempDir("example-basic-")
	defer os.RemoveAll(root)

	confDir := filepath.Join(root, "conf.d")
	mustWriteExampleFile(filepath.Join(confDir, "base", "00-app.yaml"), "server:\n  addr: \":8080\"\ndatabase:\n  pool: 10\n")
	mustWriteExampleFile(filepath.Join(confDir, "overlays", "prod", "10-app.yaml"), "server:\n  addr: \":8443\"\ndatabase:\n  pool: 32\n")

	restoreEnv := mustSetExampleEnv("APP_PROFILE", "prod")
	defer restoreEnv()

	mgr, err := fastconf.New[basicExampleConfig](context.Background(),
		fastconf.WithDir(confDir),
		fastconf.WithProfileEnv("APP_PROFILE"),
		fastconf.WithDefaultProfile("dev"),
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

func mustExampleTempDir(pattern string) string {
	dir, err := os.MkdirTemp(".", pattern)
	if err != nil {
		panic(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		panic(err)
	}
	return abs
}

func mustWriteExampleFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func mustSetExampleEnv(key, value string) func() {
	old, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		panic(err)
	}
	return func() {
		if !ok {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, old)
	}
}
