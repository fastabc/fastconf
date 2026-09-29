package consul

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCredentialRedirectIsolation(t *testing.T) {
	for _, mode := range []string{"hostname", "port", "scheme"} {
		t.Run(mode, func(t *testing.T) {
			var leaked atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Consul-Token") != "" {
					leaked.Add(1)
				}
				_, _ = fmt.Fprint(w, `[]`)
			}))
			defer target.Close()
			destination := target.URL
			if mode == "hostname" {
				destination = strings.Replace(destination, "127.0.0.1", "localhost", 1)
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Consul-Token") != "canary-token" {
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
			p, err := New(origin.URL, "app", WithToken("canary-token"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "scheme" {
				p.client.(*http.Client).Transport = origin.Client().Transport
			}
			if _, err := p.Load(context.Background()); err == nil {
				t.Error("redirect unexpectedly accepted")
			}
			if leaked.Load() != 0 {
				t.Fatal("credentials reached a different origin")
			}
		})
	}
}

func TestInjectedClientIsNotMutated(t *testing.T) {
	client := &http.Client{}
	_, err := New("http://localhost", "app", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if client.CheckRedirect != nil || client.Timeout != 0 || client.Transport != nil {
		t.Fatal("caller client mutated")
	}
}
