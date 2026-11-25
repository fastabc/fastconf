package testutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fastabc/fastconf/contracts"
)

// CredentialRedirects checks that a provider keeps its credentials at the original endpoint.
// The factory uses the origin's transport to trust the scheme test's TLS certificate.
func CredentialRedirects(t *testing.T, header, response string, provider func(*testing.T, *httptest.Server) contracts.Provider) {
	t.Helper()
	for _, mode := range []string{"hostname", "port", "scheme"} {
		t.Run(mode, func(t *testing.T) {
			var leaked atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(header) != "" {
					leaked.Add(1)
				}
				_, _ = io.WriteString(w, response)
			}))
			defer target.Close()
			destination := target.URL
			if mode == "hostname" {
				destination = strings.Replace(destination, "127.0.0.1", "localhost", 1)
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(header) != "canary-token" {
					t.Error("initial request lost credentials")
				}
				http.Redirect(w, r, destination, http.StatusTemporaryRedirect)
			})
			var origin *httptest.Server
			if mode == "scheme" {
				origin = httptest.NewTLSServer(handler)
			} else {
				origin = httptest.NewServer(handler)
			}
			defer origin.Close()
			p := provider(t, origin)
			if _, err := p.Load(context.Background()); err == nil {
				t.Error("redirect unexpectedly accepted")
			}
			if leaked.Load() != 0 {
				t.Fatal("credentials reached a different origin")
			}
		})
	}
}
