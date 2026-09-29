package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	httpprov "github.com/fastabc/fastconf/providers/http"
)

func TestHTTPProvider_DoesNotFollowRedirectWithHeaders(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); _, _ = w.Write([]byte("k: stolen")) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "private" {
			t.Error("initial request lost header")
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()
	p, err := httpprov.New("redirect", origin.URL, yamlCodec{}, httpprov.WithHeader("X-Api-Key", "private"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("want rejected 302, got %v", err)
	}
	if reached.Load() != 0 {
		t.Fatal("redirect target received a request")
	}
}
