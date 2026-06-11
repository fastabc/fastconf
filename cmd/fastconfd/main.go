// fastconfd is the sidecar daemon. It runs an embedded
// fastconf.Manager[map[string]any] and exposes the live configuration
// over a tiny HTTP API so that polyglot workloads (Python, Node,
// Rust, shell) can pull strongly-versioned config out-of-process
// without linking the Go SDK.
//
// Endpoints:
//
//	GET  /config           — current snapshot as JSON
//	GET  /config?path=a.b  — single path lookup
//	GET  /healthz          — 200 once first reload succeeded
//	GET  /version          — current generation + content hash
//	POST /reload           — manual reload (auth via X-Reload-Token)
//	GET  /events           — Server-Sent Events stream of reload causes
//
// Scope: HTTP+SSE only. A future iteration may add gRPC; the
// daemon is structured so a new transport plugs in via the same
// configRegistry abstraction.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
	"github.com/fastabc/fastconf/internal/flog"
)

// version is injected at build time via `-ldflags "-X main.version=<tag>"`
// by the dist pipeline. Default "dev" is reported when building from source
// without -ldflags (e.g. `go install`).
var version = "dev"

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	var flags cli.Flags
	cli.RegisterFlags(fs, &flags)
	// fastconfd-specific overrides: watcher is always on for a sidecar,
	// add HTTP-listen + reload-auth flags that have no analogue elsewhere.
	flags.Watch = true
	addr := fs.String("addr", ":8650", "HTTP listen address")
	token := fs.String("reload-token", os.Getenv("FASTCONFD_RELOAD_TOKEN"), "shared secret required for POST /reload")
	_ = fs.Parse(os.Args[1:])

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	log := flog.New(logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	bus := newEventBus()
	mgr, err := cli.LoadConfig[map[string]any](ctx, flags,
		fastconf.WithLogger(logger),
		fastconf.WithAuditSink(bus),
	)
	if err != nil {
		log.Error().Err(err).Msg("initial reload failed")
		os.Exit(1)
	}
	defer mgr.Close()

	srv := newServer(mgr, bus, *token, log)
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info().Str("addr", *addr).Msg("fastconfd listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("http serve")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("fastconfd shutting down")
	shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = httpSrv.Shutdown(shutdownCtx)
}
