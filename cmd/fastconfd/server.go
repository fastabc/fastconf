package main

import (
	"encoding/json"
	"net/http"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/internal/flog"
)

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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
