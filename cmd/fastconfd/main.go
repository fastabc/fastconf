// fastconfd is the sidecar daemon. It runs an embedded
// fastconf.Manager[map[string]any] and exposes the live configuration
// over a tiny HTTP API so that polyglot workloads (Python, Node,
// Rust, shell) can pull strongly-versioned config out-of-process
// without linking the Go SDK.
//
// Endpoints:
//
//	GET  /config           — current snapshot as JSON, secrets masked (see -secret-path)
//	GET  /config?path=a.b  — single path lookup
//	GET  /dump             — YAML (or ?format=json) dump, secrets masked
//	?unredacted=true       — plaintext; requires X-Unredacted-Token (-unredacted-token)
//	GET  /healthz          — 200 once first reload succeeded
//	GET  /version          — current generation + content hash
//	POST /reload           — manual reload (auth via X-Reload-Token)
//	GET  /events           — Server-Sent Events stream of reload causes
//
// Scope: HTTP and Server-Sent Events.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/cmd/internal/cli"
)

// version is injected at build time via `-ldflags "-X main.version=<tag>"`
// by the dist pipeline. Default "dev" is reported when building from source
// without -ldflags (e.g. `go install`).
var version = "dev"

// defaultSecretPaths masks common credential keys at any depth. The sidecar
// serves map[string]any, which carries no fc:"secret" tags, so without
// path patterns every redacted view would be plaintext. -secret-path adds
// patterns on top of these.
var defaultSecretPaths = []string{
	"**.password", "**.passwd", "**.secret", "**.token",
	"**.api_key", "**.apikey", "**.private_key",
}

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	var flags cli.Flags
	cli.RegisterFlags(fs, &flags)
	// fastconfd-specific overrides: watcher is always on for a sidecar,
	// add HTTP-listen + reload-auth flags that have no analogue elsewhere.
	flags.Watch = true
	addr := fs.String("addr", "127.0.0.1:8650", "HTTP listen address")
	token := fs.String("reload-token", os.Getenv("FASTCONFD_RELOAD_TOKEN"), "shared secret required for POST /reload")
	readToken := fs.String("read-token", os.Getenv("FASTCONFD_READ_TOKEN"), "shared secret required for config, dump and events")
	unredactedToken := fs.String("unredacted-token", os.Getenv("FASTCONFD_UNREDACTED_TOKEN"), "shared secret for ?unredacted=true on config and dump (empty disables plaintext)")
	secretPaths := append([]string(nil), defaultSecretPaths...)
	fs.Func("secret-path", "dotted path pattern to redact (repeatable; * = one segment, ** = any)", func(v string) error {
		secretPaths = append(secretPaths, v)
		return nil
	})
	_ = fs.Parse(os.Args[1:])
	if err := authGate(*token, *readToken); err != nil {
		fmt.Fprintln(os.Stderr, "fastconfd: "+err.Error())
		os.Exit(2)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	bus := newEventBus()
	mgr, err := cli.LoadConfig[map[string]any](ctx, flags,
		fastconf.WithLogger(log),
		fastconf.WithObserver(bus),
		fastconf.WithSecretPaths(secretPaths...),
	)
	if err != nil {
		log.LogAttrs(context.Background(), slog.LevelError, "initial reload failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer func() { _ = mgr.Close() }()

	srv := newServer(mgr, bus, *token, log)
	srv.readToken = *readToken
	srv.unredactedToken = *unredactedToken
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.LogAttrs(context.Background(), slog.LevelInfo, "fastconfd listening", slog.String("addr", *addr))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.LogAttrs(context.Background(), slog.LevelError, "http serve", slog.Any("err", err))
		}
	}()

	<-ctx.Done()
	log.LogAttrs(context.Background(), slog.LevelInfo, "fastconfd shutting down")
	shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// authGate is the daemon's startup authentication policy: both tokens are
// required, whatever the listen address.
//
// Binding to loopback is not treated as a waiver. Every process sharing the
// host or the pod's network namespace can reach 127.0.0.1, so the address
// does not establish the single-tenant boundary it suggests, and /config and
// /dump serve the full merged configuration.
func authGate(token, readToken string) error {
	if token == "" || readToken == "" {
		return errors.New("-reload-token and -read-token are required")
	}
	return nil
}
