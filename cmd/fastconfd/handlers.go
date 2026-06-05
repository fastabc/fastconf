package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/mappath"
)

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
		"generation": st.Generation(),
		"hash":       st.Hash(),
		"loaded_at":  st.LoadedAt(),
		"reason":     st.Cause().Reason,
	})
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.mgr.Get()
	if cfg == nil {
		http.Error(w, "no state", http.StatusServiceUnavailable)
		return
	}
	// Opt-in redaction via ?redact=true. Uses the Manager's configured
	// SecretRedactor (DefaultSecretRedactor when none set).
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
// when the query parameter format=json is set.
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
	b, err := st.Dump(fastconf.DumpYAML, nil)
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
	if s.token != "" {
		got := r.Header.Get("X-Reload-Token")
		// Constant-time compare to avoid a byte-by-byte timing oracle on
		// the reload secret. ConstantTimeCompare returns 0 on length
		// mismatch without examining bytes, which is acceptable for a
		// fixed-length token.
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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
