package vault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/fastabc/fastconf/contracts"
)

// ResolverPrefix identifies a Vault KV v2 field reference.
const ResolverPrefix = "ref+vault://"

// Resolver reads one KV v2 scalar per reference, sharing Provider's HTTP/auth options.
type Resolver struct{ provider *Provider }

// NewResolver validates options without issuing requests. The application owns credentials.
func NewResolver(addr, token string, opts ...Option) (*Resolver, error) {
	p, err := New(addr, "-", token, opts...)
	if err != nil {
		return nil, err
	}
	return &Resolver{provider: p}, nil
}

// Resolve reads body in the form path#key. Error messages exclude reference
// material and response values. Dynamic credentials re-login once after 403.
func (r *Resolver) Resolve(ctx context.Context, body string) (string, error) {
	path, key, ok := strings.Cut(body, "#")
	if !ok || path == "" || key == "" {
		return "", errors.New("vault resolver: missing #key")
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("vault resolver: invalid path")
		}
		parts[i] = url.PathEscape(part)
	}
	path = strings.Join(parts, "/")
	p := r.provider
	if err := p.ensureToken(ctx); err != nil {
		return "", contracts.SafeError("vault resolver: login failed", err)
	}
	data, _, err := p.readDataAt(ctx, path)
	var status *dataStatusError
	if p.auth != nil && errors.As(err, &status) && status.code == http.StatusForbidden {
		p.mu.Lock()
		p.token = ""
		p.mu.Unlock()
		if err := p.ensureToken(ctx); err != nil {
			return "", contracts.SafeError("vault resolver: login failed", err)
		}
		data, _, err = p.readDataAt(ctx, path)
	}
	if err != nil {
		return "", contracts.SafeError("vault resolver: read failed", err)
	}
	value, ok := data[key]
	if !ok {
		return "", errors.New("vault resolver: key not found")
	}
	switch v := value.(type) {
	case string:
		return v, nil
	case bool, float64:
		return fmt.Sprint(v), nil
	default:
		return "", errors.New("vault resolver: value is not a scalar")
	}
}
