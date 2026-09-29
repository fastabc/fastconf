package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/confmap"
)

func (s *server) authorizedRead(r *http.Request) bool {
	if s.readToken == "" {
		return true
	} // embedded callers may opt in explicitly.
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Config-Token")), []byte(s.readToken)) == 1
}

// plaintext reports whether r asked for unredacted output. It writes 401
// and returns ok=false when the request asks without the unredacted token;
// an empty -unredacted-token disables plaintext output entirely.
func (s *server) plaintext(w http.ResponseWriter, r *http.Request) (plain, ok bool) {
	if r.URL.Query().Get("unredacted") != "true" {
		return false, true
	}
	if s.unredactedToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Unredacted-Token")), []byte(s.unredactedToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false, false
	}
	return true, true
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
		"generation": st.Generation(),
		"hash":       st.Hash(),
		"loaded_at":  st.Cause().At,
		"reason":     st.Cause().Reason,
	})
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !s.authorizedRead(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	st := s.mgr.Snapshot()
	if st == nil {
		http.Error(w, "no state", http.StatusServiceUnavailable)
		return
	}
	plain, ok := s.plaintext(w, r)
	if !ok {
		return
	}
	tree := st.Map()
	if plain {
		tree = st.Unredacted().Map()
	}
	if path := r.URL.Query().Get("path"); path != "" {
		v, ok := confmap.GetDotted(tree, path)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

// handleDump returns the current merged state as YAML (default) or JSON
// when the query parameter format=json is set. Secrets are masked unless
// the request passes the unredacted gate.
func (s *server) handleDump(w http.ResponseWriter, r *http.Request) {
	if !s.authorizedRead(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	st := s.mgr.Snapshot()
	if st == nil {
		http.Error(w, "no state", http.StatusServiceUnavailable)
		return
	}
	plain, ok := s.plaintext(w, r)
	if !ok {
		return
	}
	dump := st.Dump
	if plain {
		dump = st.Unredacted().Dump
	}
	format := r.URL.Query().Get("format")
	if format == "json" {
		b, err := dump(fastconf.JSON)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	b, err := dump(fastconf.YAML)
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
	if s.token == "" {
		http.Error(w, "reload authentication is not configured", http.StatusServiceUnavailable)
		return
	}
	{
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
	if !s.authorizedRead(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
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
	// Complete the handshake even when no configuration changes are pending.
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case cause, ok := <-c:
			if !ok {
				return
			}
			payload, _ := json.Marshal(cause)
			if _, err := fmt.Fprintf(w, "event: reload\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
