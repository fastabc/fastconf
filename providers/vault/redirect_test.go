package vault

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/testutil"
)

func TestCredentialRedirectIsolation(t *testing.T) {
	testutil.CredentialRedirects(t, "X-Vault-Token", `{"data":{"data":{"value":"ok"},"metadata":{"version":1}}}`, func(t *testing.T, origin *httptest.Server) contracts.Provider {
		t.Helper()
		p, err := New(origin.URL, "app", "canary-token")
		if err != nil {
			t.Fatal(err)
		}
		p.client.(*http.Client).Transport = origin.Client().Transport
		return p
	})
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
