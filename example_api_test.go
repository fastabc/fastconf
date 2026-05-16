package fastconf_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing/fstest"

	"github.com/fastabc/fastconf"
)

type apiExampleConfig struct {
	Server struct {
		Addr string `json:"addr" yaml:"addr"`
	} `json:"server" yaml:"server"`
}

// ExampleNew demonstrates the shortest typed entry path: construct a manager,
// read the live value, and close it when the owner shuts down.
func ExampleNew() {
	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.PresetTesting(fastconf.TestingOpts{
			FS: fstest.MapFS{
				"conf.d/base/00-app.yaml": &fstest.MapFile{
					Data: []byte("server:\n  addr: \":8080\"\n"),
				},
			},
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	fmt.Println(mgr.Get().Server.Addr)
	// Output:
	// :8080
}

// ExampleSubscribe demonstrates reacting to a typed subtree after a successful
// commit while keeping the caller in charge of what counts as "changed".
func ExampleSubscribe() {
	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.PresetTesting(fastconf.TestingOpts{
			FS: fstest.MapFS{
				"conf.d/base/00-app.yaml": &fstest.MapFile{
					Data: []byte("server:\n  addr: \":8080\"\n"),
				},
			},
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	cancel := fastconf.Subscribe(mgr,
		func(c *apiExampleConfig) *string { return &c.Server.Addr },
		func(old, next *string) {
			if old != nil && next != nil && *old != *next {
				fmt.Printf("%s -> %s\n", *old, *next)
			}
		},
	)
	defer cancel()

	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"server": map[string]any{"addr": ":9090"},
	}))
	// Output:
	// :8080 -> :9090
}

// ExampleManager_Errors demonstrates the asynchronous failure stream that lets
// services centralize reload error handling without blocking the writer.
func ExampleManager_Errors() {
	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.PresetTesting(fastconf.TestingOpts{
			FS: fstest.MapFS{
				"conf.d/base/00-app.yaml": &fstest.MapFile{
					Data: []byte("server:\n  addr: \":8080\"\n"),
				},
			},
		}),
		fastconf.WithValidator(func(c *apiExampleConfig) error {
			if c.Server.Addr == "" {
				return fmt.Errorf("server.addr is required")
			}
			return nil
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	_ = mgr.Reload(context.Background(), fastconf.WithSourceOverride(map[string]any{
		"server": map[string]any{"addr": ""},
	}))
	re := <-mgr.Errors()
	fmt.Println(re.Reason, re.Err != nil)
	// Output:
	// override true
}

// ExampleManager_Plan demonstrates previewing a file-backed change before it
// becomes the live snapshot.
func ExampleManager_Plan() {
	root := mustExampleTempDir("example-plan-")
	defer os.RemoveAll(root)

	confDir := filepath.Join(root, "conf.d")
	configPath := filepath.Join(confDir, "base", "00-app.yaml")
	mustWriteExampleFile(configPath, "server:\n  addr: \":8080\"\n")

	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithDir(confDir),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	mustWriteExampleFile(configPath, "server:\n  addr: \":9090\"\n")
	plan, err := mgr.Plan().Run(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(len(plan.Diff), plan.Proposed.Value.Server.Addr, mgr.Get().Server.Addr)
	// Output:
	// 1 :9090 :8080
}

// ExampleReplay_Rollback demonstrates recovering a retained prior snapshot
// without rerunning the reload pipeline.
func ExampleReplay_Rollback() {
	root := mustExampleTempDir("example-replay-")
	defer os.RemoveAll(root)

	confDir := filepath.Join(root, "conf.d")
	configPath := filepath.Join(confDir, "base", "00-app.yaml")
	mustWriteExampleFile(configPath, "server:\n  addr: \":8080\"\n")

	mgr, err := fastconf.New[apiExampleConfig](context.Background(),
		fastconf.WithDir(confDir),
		fastconf.WithHistory(2),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mgr.Close()

	mustWriteExampleFile(configPath, "server:\n  addr: \":9090\"\n")
	if err := mgr.Reload(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	liveAfterReload := mgr.Get().Server.Addr
	history := mgr.Replay().List()
	if err := mgr.Replay().Rollback(history[0]); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(liveAfterReload, mgr.Get().Server.Addr)
	// Output:
	// :9090 :8080
}
