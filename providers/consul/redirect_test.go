package consul

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/testutil"
)

func TestCredentialRedirectIsolation(t *testing.T) {
	testutil.CredentialRedirects(t, "X-Consul-Token", `[]`, func(t *testing.T, origin *httptest.Server) contracts.Provider {
		t.Helper()
		p, err := New(origin.URL, "app", WithToken("canary-token"))
		if err != nil {
			t.Fatal(err)
		}
		p.client.(*http.Client).Transport = origin.Client().Transport
		return p
	})
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
