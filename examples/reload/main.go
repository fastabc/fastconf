// Command reload previews local file changes, rejects invalid input, and rolls back.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
)

//go:embed config/*.yaml
var files embed.FS

type Config struct {
	Port int `json:"port" fc:"min=1,max=65535"`
}

func run(out io.Writer) error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "fastconf-reload-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "app.yaml")
	write := func(name string) error {
		data, err := files.ReadFile("config/" + name + ".yaml")
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	if err := write("initial"); err != nil {
		return err
	}
	// No automatic watcher: each change is inspected before manual publication.
	mgr, err := fastconf.New[Config](ctx,
		fastconf.WithDir(dir),
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithHistory(4),
	)
	if err != nil {
		return err
	}
	defer func() { _ = mgr.Close() }()
	initial := mgr.Snapshot()
	if err := write("invalid"); err != nil {
		return err
	}
	preview, err := mgr.Plan(ctx)
	if err != nil {
		return err
	}
	violations := 0
	for _, report := range preview.Validators {
		if report.Err != nil {
			violations++
		}
	}
	if _, err := fmt.Fprintf(out, "invalid preview: violations=%d live=%d\n", violations, mgr.Get().Port); err != nil {
		return err
	}
	if err := mgr.Reload(ctx); !errors.Is(err, fastconf.ErrInvalid) {
		return fmt.Errorf("expected validation failure, got %v", err)
	}
	if _, err := fmt.Fprintf(out, "rejected: preserved=%t\n", mgr.Snapshot() == initial); err != nil {
		return err
	}
	if err := write("proposed"); err != nil {
		return err
	}
	preview, err = mgr.Plan(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "valid preview: proposed=%d live=%d changes=%d\n", preview.Proposed.Value().Port, mgr.Get().Port, len(preview.Diff)); err != nil {
		return err
	}
	if err := mgr.Reload(ctx); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "committed: port=%d generation=%d\n", mgr.Get().Port, mgr.Snapshot().Generation()); err != nil {
		return err
	}
	if err := mgr.History().Rollback(initial); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "rollback: port=%d generation=%d\n", mgr.Get().Port, mgr.Snapshot().Generation())
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
