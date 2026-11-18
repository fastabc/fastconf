package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/fastabc/fastconf"
)

type server struct {
	mgr       *fastconf.Manager[map[string]any]
	bus       *eventBus
	token     string
	readToken string
	stopping  <-chan struct{}
	// unredactedToken gates ?unredacted=true on /config and /dump; empty
	// disables plaintext output.
	unredactedToken string
	log             *slog.Logger
}

func newServer(mgr *fastconf.Manager[map[string]any], bus *eventBus, token string, log *slog.Logger) *server {
	if log == nil {
		log = slog.Default()
	}
	return &server{mgr: mgr, bus: bus, token: token, log: log}
}

func (s *server) httpServer(addr string) *http.Server {
	streams, stopStreams := context.WithCancel(context.Background())
	s.stopping = streams.Done()
	srv := &http.Server{Addr: addr, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	srv.RegisterOnShutdown(stopStreams)
	return srv
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
