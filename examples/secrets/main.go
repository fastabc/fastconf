// Command secrets demonstrates field resolvers without a Vault service or real credentials.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
	"github.com/fastabc/fastconf/providers/vault"
)

type Config struct {
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
}

// Supply an application-owned HTTP client in production. This fixture performs
// no network I/O; it exercises the real Vault KV v2 resolver and request shape.
type vaultFixture struct{}

func (vaultFixture) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Path != "/v1/secret/data/app/db" || r.Header.Get("X-Vault-Token") != "demo-only" {
		return nil, errors.New("unexpected Vault request")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"data":{"data":{"password":"demo-vault-password"}}}`))}, nil
}

func run(out io.Writer) error {
	ctx := context.Background()
	v, err := vault.NewResolver("https://vault.example.invalid", "demo-only", vault.WithClient(vaultFixture{}))
	if err != nil {
		return err
	}
	key, unavailable := "demo-key-one", false
	local := fastconf.PrefixSecretResolver("local", "ref+local://", func(_ context.Context, name string) (string, error) {
		if unavailable {
			return "", errors.New("local key unavailable")
		}
		if name != "api" {
			return "", errors.New("unknown key")
		}
		return key, nil
	})
	m, err := fastconf.New[Config](ctx, fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("refs", "json", []byte(`{"password":"ref+vault://app/db#password","api_key":"ref+local://api"}`))),
		fastconf.WithSecretResolvers(local, fastconf.PrefixSecretResolver("vault", vault.ResolverPrefix, v.Resolve)),
		fastconf.WithProvenance(fastconf.ProvenanceFull))
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	initial := m.Snapshot()
	// Get/Value are for application use and contain plaintext. Never log them.
	ready := m.Get().Password != "" && m.Get().APIKey != ""
	if _, err := fmt.Fprintf(out, "runtime-ready=%t diagnostics=%v\n", ready, initial.Map()); err != nil {
		return err
	}
	key = "demo-key-two"
	preview, err := m.Plan(ctx)
	if err != nil {
		return err
	}
	if err := preview.Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "preview changes=%d live-preserved=%t\n", len(preview.Diff), m.Snapshot() == initial); err != nil {
		return err
	}
	if err := m.Reload(ctx); err != nil {
		return err
	}
	for _, line := range fastconf.FormatDiff(initial.Diff(m.Snapshot())) {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	before := m.Snapshot()
	unavailable = true
	if err := m.Reload(ctx); !errors.Is(err, fastconf.ErrTransform) {
		return fmt.Errorf("expected resolution failure, got %v", err)
	}
	_, err = fmt.Fprintf(out, "resolution failed: preserved=%t\n", m.Snapshot() == before)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
