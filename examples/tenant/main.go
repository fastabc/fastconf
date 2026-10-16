// Command tenant demonstrates isolated managers backed by editable local files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/env"
	"github.com/fastabc/fastconf/providers/source"
)

type Config struct {
	Server struct {
		Addr    string        `json:"addr" fc:"required"`
		Timeout time.Duration `json:"timeout"`
	} `json:"server"`
	Database struct {
		DSN  string `json:"dsn" fc:"required,secret"`
		Pool int    `json:"pool" fc:"min=1,max=100"`
	} `json:"database"`
	Limits struct {
		RPS int `json:"rps" fc:"min=1"`
	} `json:"limits"`
}

func run(ctx context.Context, dir string, out io.Writer) error {
	// The registry is owned by this function; no concurrent insertion or eviction.
	tenants := map[string]*fastconf.Manager[Config]{}
	defer func() {
		for _, mgr := range tenants {
			_ = mgr.Close()
		}
	}()
	for _, tenant := range []struct{ id, prefix string }{
		{"tenant-a", "FC_TENANT_A_"},
		{"tenant-b", "FC_TENANT_B_"},
	} {
		path := filepath.Join(dir, "tenants", tenant.id+".yaml")
		// source.NewFile treats a missing file as optional. Here each tenant's
		// file is required, so check it before constructing its manager.
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("tenant %q: %w", tenant.id, err)
		}
		mgr, err := fastconf.New[Config](ctx,
			fastconf.WithDir(dir),
			fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
			fastconf.WithTenant(tenant.id), // labels causes; does not select files
			fastconf.WithProvider(
				source.NewFile(path),   // above base and profile files
				env.New(tenant.prefix), // only this tenant's environment
			),
			fastconf.WithUnknownFields(fastconf.UnknownError),
			fastconf.WithProvenance(fastconf.ProvenanceFull),
			fastconf.WithHistory(4),
		)
		if err != nil {
			return fmt.Errorf("tenant %q: %w", tenant.id, err)
		}
		tenants[tenant.id] = mgr
		cfg := mgr.Get()
		if _, err := fmt.Fprintf(out, "%s addr=%s timeout=%s pool=%d rps=%d\n",
			mgr.Snapshot().Cause().Tenant, cfg.Server.Addr, cfg.Server.Timeout, cfg.Database.Pool, cfg.Limits.RPS); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%s dsn=%v\n", tenant.id, mgr.Snapshot().Map()["database"].(map[string]any)["dsn"]); err != nil {
			return err
		}
	}

	a, b := tenants["tenant-a"], tenants["tenant-b"]
	beforeA, beforeB := a.Snapshot(), b.Snapshot()
	err := a.Reload(ctx, fastconf.WithOverride(map[string]any{
		"database": map[string]any{"pool": 0},
	}))
	if !errors.Is(err, fastconf.ErrInvalid) {
		return fmt.Errorf("expected tenant-a validation failure, got %v", err)
	}
	if _, err := fmt.Fprintf(out, "tenant-a rejected=%t preserved=%t tenant-b untouched=%t\n",
		err != nil, a.Snapshot() == beforeA, b.Snapshot() == beforeB); err != nil {
		return err
	}
	if err := b.Reload(ctx, fastconf.WithOverride(map[string]any{
		"limits": map[string]any{"rps": 250},
	})); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "tenant-b updated rps=%d tenant-a rps=%d\n", b.Get().Limits.RPS, a.Get().Limits.RPS)
	return err
}

func main() {
	dir := flag.String("dir", "examples/tenant/conf.d", "local configuration directory")
	flag.Parse()
	if err := run(context.Background(), *dir, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
