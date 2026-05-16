// fastconfd is the Phase 26 sidecar daemon. It runs an embedded
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
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fastabc/fastconf/cmd/internal/cli"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/flog"
	"github.com/fastabc/fastconf/pkg/mappath"
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

// eventBus is a tiny pub/sub fed by the AuditSink contract; SSE
// subscribers read from a buffered channel each.
type eventBus struct {
	mu       sync.Mutex
	subs     map[chan fastconf.ReloadCause]struct{}
	lastOK   bool
	lastTime time.Time
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[chan fastconf.ReloadCause]struct{})}
}

// Audit implements fastconf.AuditSink.
func (b *eventBus) Audit(_ context.Context, cause fastconf.ReloadCause) error {
	b.mu.Lock()
	b.lastOK = true
	b.lastTime = time.Now()
	subs := make([]chan fastconf.ReloadCause, 0, len(b.subs))
	for c := range b.subs {
		subs = append(subs, c)
	}
	b.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- cause:
		default:
		}
	}
	return nil
}

func (b *eventBus) subscribe() chan fastconf.ReloadCause {
	c := make(chan fastconf.ReloadCause, 8)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c
}

func (b *eventBus) unsubscribe(c chan fastconf.ReloadCause) {
	b.mu.Lock()
	delete(b.subs, c)
	b.mu.Unlock()
	close(c)
}

type server struct {
	mgr   *fastconf.Manager[map[string]any]
	bus   *eventBus
	token string
	log   *flog.Logger
}

func newServer(mgr *fastconf.Manager[map[string]any], bus *eventBus, token string, log *flog.Logger) *server {
	return &server{mgr: mgr, bus: bus, token: token, log: log}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/dump", s.handleDump)
	mux.HandleFunc("/reload", s.handleReload)
	mux.HandleFunc("/events", s.handleEvents)
	return mux
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if s.mgr.Get() == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	st := s.mgr.Snapshot()
	if st == nil {
		http.Error(w, "no state", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    version,
		"generation": st.Generation,
		"hash":       st.Hash,
		"loaded_at":  st.LoadedAt,
		"reason":     st.Cause.Reason,
	})
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.mgr.Get()
	if cfg == nil {
		http.Error(w, "no state", http.StatusServiceUnavailable)
		return
	}
	// BUG-1207: opt-in redaction via ?redact=true. Uses the Manager's
	// configured SecretRedactor (DefaultSecretRedactor when none set).
	if r.URL.Query().Get("redact") == "true" {
		redacted := s.mgr.Snapshot().Redacted()
		if path := r.URL.Query().Get("path"); path != "" {
			v, ok := mappath.GetDotted(redacted, path)
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
		writeJSON(w, http.StatusOK, redacted)
		return
	}
	if path := r.URL.Query().Get("path"); path != "" {
		v, ok := mappath.GetDotted(*cfg, path)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	writeJSON(w, http.StatusOK, *cfg)
}

// handleDump returns the current merged state as YAML (default) or JSON
// when the query parameter format=json is set. Phase 134.
func (s *server) handleDump(w http.ResponseWriter, r *http.Request) {
	st := s.mgr.Snapshot()
	if st == nil {
		http.Error(w, "no state", http.StatusServiceUnavailable)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "json" {
		writeJSON(w, http.StatusOK, st.Introspect().Settings())
		return
	}
	b, err := st.MarshalYAML(nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(b)
}

func (s *server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.token != "" && r.Header.Get("X-Reload-Token") != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := s.mgr.Reload(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte("reloaded"))
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	c := s.bus.subscribe()
	defer s.bus.unsubscribe(c)
	for {
		select {
		case <-r.Context().Done():
			return
		case cause, ok := <-c:
			if !ok {
				return
			}
			payload, _ := json.Marshal(cause)
			fmt.Fprintf(w, "event: reload\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
