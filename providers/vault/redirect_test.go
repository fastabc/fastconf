package vault

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
				if r.Header.Get("X-Vault-Token") != "" {
					leaked.Add(1)
				}
				_, _ = fmt.Fprint(w, `{"data":{"data":{"value":"ok"},"metadata":{"version":1}}}`)
			}))
			defer target.Close()
			destination := target.URL
			if mode == "hostname" {
				destination = strings.Replace(destination, "127.0.0.1", "localhost", 1)
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Vault-Token") != "canary-token" {
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
			p, err := New(origin.URL, "app", "canary-token")
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
	_, err := New("http://localhost", "app", "token", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if client.CheckRedirect != nil || client.Timeout != 0 || client.Transport != nil {
		t.Fatal("caller client mutated")
	}
}

func TestAppRoleDoesNotReplaySecretOnRedirect(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = fmt.Fprint(w, `{"auth":{"client_token":"token","lease_duration":60}}`)
			}))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, status)
			}))
			defer origin.Close()
			if _, _, err := AppRoleAuth(origin.URL, "role", "canary-secret", nil).Login(context.Background()); err == nil {
				t.Error("redirect unexpectedly accepted")
			}
			if requests.Load() != 0 {
				t.Fatal("AppRole body replayed across origins")
			}
		})
	}
}
