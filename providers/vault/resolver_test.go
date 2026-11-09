package vault_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
	"github.com/fastabc/fastconf/providers/vault"
)

func TestResolver_Resolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secret/data/app/db" || r.Header.Get("X-Vault-Token") != "token" {
			t.Errorf("request: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"data":{"password":"pw","port":5432,"enabled":true,"large":9007199254740993,"decimal":1.234567890123456789,"object":{"secret":"pw"}}}}`)
	}))
	defer srv.Close()
	r, err := vault.NewResolver(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{{"app/db#password", "pw"}, {"app/db#port", "5432"}, {"app/db#enabled", "true"}, {"app/db#large", "9007199254740993"}, {"app/db#decimal", "1.234567890123456789"}} {
		got, err := r.Resolve(context.Background(), tc.body)
		if err != nil || got != tc.want {
			t.Fatalf("%q %v", got, err)
		}
	}
	for _, tc := range []struct{ body, want string }{{"app/db", "missing #key"}, {"app/db#nope", "not found"}, {"app/db#object", "scalar"}} {
		_, err := r.Resolve(context.Background(), tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "pw") {
			t.Fatalf("%v", err)
		}
	}
	type C struct {
		Password string `json:"password"`
	}
	st, err := fastconf.Load[C](context.Background(), fastconf.WithProvider(source.NewBytes("refs", "application/json", []byte(`{"password":"ref+vault://app/db#password"}`))), fastconf.WithSecretResolvers(fastconf.PrefixSecretResolver("vault", vault.ResolverPrefix, r.Resolve)))
	if err != nil {
		t.Fatal(err)
	}
	if st.Value().Password != "pw" || st.Map()["password"] != "***REDACTED***" {
		t.Fatal(st.Map())
	}
}
func TestResolver_ReloginOn403(t *testing.T) {
	var calls, logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(403)
			_, _ = fmt.Fprint(w, "pw")
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"data":{"password":"pw"}}}`)
	}))
	defer srv.Close()
	auth := vault.AuthFunc(func(context.Context) (string, time.Duration, error) { logins.Add(1); return "token", time.Hour, nil })
	r, err := vault.NewResolver(srv.URL, "", vault.WithAuth(auth))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), "app/db#password")
	if err != nil || got != "pw" || logins.Load() != 2 {
		t.Fatalf("%q %v logins=%d", got, err, logins.Load())
	}
}
func TestResolver_SafeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, "sensitive-response")
	}))
	defer srv.Close()
	r, _ := vault.NewResolver(srv.URL, "token")
	if _, err := r.Resolve(context.Background(), "app/db#password"); err == nil || strings.Contains(err.Error(), "sensitive-response") {
		t.Fatal(err)
	}
	if _, err := vault.NewResolver(srv.URL, ""); err == nil {
		t.Fatal("accepted missing credentials")
	}
}
