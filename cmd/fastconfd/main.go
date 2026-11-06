// Command fastconfd serves configuration snapshots through authenticated HTTP and SSE.
// See docs/cookbook/deployment/sidecar.md for endpoints, tokens and deployment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
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

func main() {
	f, err := parseFlags(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		os.Exit(2)
	}
	flags := f.cli
	flags.Watch = true

	if err := authGate(f.token, f.readToken); err != nil {
		fmt.Fprintln(os.Stderr, "fastconfd: "+err.Error())
		os.Exit(2)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ln, err := listen(f.addr)
	if err != nil {
		log.LogAttrs(context.Background(), slog.LevelError, "listen failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer func() { _ = ln.Close() }()

	bus := newEventBus()
	mgr, err := cli.NewManager[map[string]any](ctx, flags,
		fastconf.WithLogger(log),
		fastconf.WithObserver(bus),
		fastconf.WithSecretPaths(f.secretPaths...),
	)
	if err != nil {
		log.LogAttrs(context.Background(), slog.LevelError, "initial reload failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer func() { _ = mgr.Close() }()

	srv := newServer(mgr, bus, f.token, log)
	srv.readToken = f.readToken
	srv.unredactedToken = f.unredactedToken
	httpSrv := &http.Server{
		Addr:              f.addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.LogAttrs(context.Background(), slog.LevelInfo, "fastconfd listening", slog.String("addr", f.addr))
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.LogAttrs(context.Background(), slog.LevelError, "http serve", slog.Any("err", err))
			cancel()
		}
	}()

	<-ctx.Done()
	log.LogAttrs(context.Background(), slog.LevelInfo, "fastconfd shutting down")
	shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func listen(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// authGate requires read and reload tokens even on loopback: other host/pod
// processes can reach it, and diagnostic endpoints expose the merged configuration.
func authGate(token, readToken string) error {
	if token == "" || readToken == "" {
		return errors.New("-reload-token and -read-token are required")
	}
	return nil
}

type daemonFlags struct {
	cli                                     cli.Flags
	addr, token, readToken, unredactedToken string
	secretPaths                             []string
}

func parseFlags(args []string, getenv func(string) string, stderr io.Writer) (daemonFlags, error) {
	var f daemonFlags
	fs := flag.NewFlagSet("fastconfd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cli.RegisterFlags(fs, &f.cli)
	fs.StringVar(&f.addr, "addr", "127.0.0.1:8650", "HTTP listen address")
	fs.StringVar(&f.token, "reload-token", "", "shared secret for POST /reload (default $FASTCONFD_RELOAD_TOKEN)")
	fs.StringVar(&f.readToken, "read-token", "", "shared secret for config, dump and events (default $FASTCONFD_READ_TOKEN)")
	fs.StringVar(&f.unredactedToken, "unredacted-token", "", "shared secret for plaintext (default $FASTCONFD_UNREDACTED_TOKEN; empty disables plaintext)")
	fs.Func("secret-path", "dotted path pattern to redact (repeatable; * = one segment, ** = any)", func(v string) error { f.secretPaths = append(f.secretPaths, v); return nil })
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.token == "" {
		f.token = getenv("FASTCONFD_RELOAD_TOKEN")
	}
	if f.readToken == "" {
		f.readToken = getenv("FASTCONFD_READ_TOKEN")
	}
	if f.unredactedToken == "" {
		f.unredactedToken = getenv("FASTCONFD_UNREDACTED_TOKEN")
	}
	return f, nil
}
