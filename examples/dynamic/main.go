// Command dynamic watches one file and retains the last valid configuration.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
)

type Config struct {
	Port int `json:"port" yaml:"port" fc:"min=1,max=65535"`
}

func newManager(ctx context.Context, path string) (*fastconf.Manager[Config], error) {
	return fastconf.New[Config](ctx,
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithUnknownFields(fastconf.UnknownError),
		fastconf.WithWatch(fastconf.Watch{}),
	)
}

func run(ctx context.Context, path string) error {
	mgr, err := newManager(ctx, path)
	if err != nil {
		return err
	}
	unsubscribe := fastconf.Subscribe(mgr, func(c *Config) *int { return &c.Port },
		func(_, next *int) { log.Printf("committed port=%d", *next) })
	log.Printf("initial port=%d", mgr.Get().Port)
	// The owning goroutine handles errors and shutdown. Callbacks only report.
	for {
		select {
		case failure := <-mgr.Errors():
			log.Printf("rejected: %v; retained port=%d", failure.Err, mgr.Get().Port)
		case <-ctx.Done():
			err := mgr.Close()
			unsubscribe()
			return err
		}
	}
}

func main() {
	path := flag.String("file", "examples/single-file/app.yaml", "configuration file")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *path); err != nil {
		log.Fatal(fmt.Errorf("dynamic: %w", err))
	}
}
