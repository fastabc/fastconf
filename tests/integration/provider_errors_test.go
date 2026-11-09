package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/consul"
	httpprov "github.com/fastabc/fastconf/providers/http"
	"github.com/fastabc/fastconf/providers/vault"
)

func TestAppRoleAuth_RejectsIncompleteAndUnsafeResponses(t *testing.T) {
	const valid = `{"auth":{"client_token":"token","lease_duration":60}}`
	const canary = "123456789012345678901234567890"
	readErr := errors.New("private transport detail")
	for _, tc := range []struct {
		name  string
		body  io.Reader
		cause error
	}{
		{"read failure", io.MultiReader(strings.NewReader(valid), iotest.ErrReader(readErr)), readErr},
		{"oversized", strings.NewReader(valid + strings.Repeat(" ", 64<<10) + "trailing"), contracts.ErrTooLarge},
		{"decode failure", strings.NewReader(`{"auth":{"client_token":"token","lease_duration":` + canary + `}}`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := vault.AppRoleAuth("https://vault.test", "role", "secret", errorDoer(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(tc.body)}, nil
			}))
			token, _, err := auth.Login(context.Background())
			if err == nil || token != "" {
				t.Fatalf("unsafe response accepted: token=%q, err=%v", token, err)
			}
			if strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), readErr.Error()) {
				t.Fatalf("response details leaked: %v", err)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("lost response cause: %v", err)
			}
			if tc.name == "decode failure" {
				var typeErr *json.UnmarshalTypeError
				if !errors.As(err, &typeErr) {
					t.Fatalf("lost decoder cause: %v", err)
				}
			}
		})
	}
}

type errorDoer func(*http.Request) (*http.Response, error)

func (f errorDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

type errorDecoder struct{ err error }

func (d errorDecoder) Decode([]byte) (map[string]any, error) { return nil, d.err }

func TestRemoteProviderErrors_HideSecretsAndPreserveCauses(t *testing.T) {
	const canary = "private-response-and-url-token"
	cause := errors.New(canary)
	for _, mode := range []string{"transport", "response", "decode"} {
		client := errorDoer(func(*http.Request) (*http.Response, error) {
			if mode == "transport" {
				return nil, cause
			}
			status := http.StatusOK
			if mode == "response" {
				status = http.StatusForbidden
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(canary))}, nil
		})
		for _, name := range []string{"http", "vault", "consul"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				var p contracts.Provider
				var err error
				switch name {
				case "http":
					p, err = httpprov.New("remote", "https://example.test/config?token="+canary, errorDecoder{cause}, httpprov.WithClient(client))
				case "vault":
					p, err = vault.New("https://example.test", "app", "token", vault.WithClient(client))
				case "consul":
					p, err = consul.New("https://example.test", "/app", consul.WithClient(client))
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = p.Load(context.Background())
				if err == nil || strings.Contains(err.Error(), canary) {
					t.Fatalf("unsafe or missing error: %v", err)
				}
				if (mode == "transport" || (mode == "decode" && name == "http")) && !errors.Is(err, cause) {
					t.Fatalf("lost original cause: %v", err)
				}
			})
		}
	}
	_, _, err := vault.AppRoleAuth("https://example.test", "role", "secret", errorDoer(func(*http.Request) (*http.Response, error) {
		return nil, cause
	})).Login(context.Background())
	if err == nil || strings.Contains(err.Error(), canary) || !errors.Is(err, cause) {
		t.Fatalf("unsafe AppRole error or lost cause: %v", err)
	}
}
