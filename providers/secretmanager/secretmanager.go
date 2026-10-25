// Package secretmanager loads one GCP Secret Manager version as a configuration
// document. It does not resolve references in other configuration sources.
package secretmanager

import (
	"context"
	"errors"
	"hash/crc32"
	"strings"

	sdk "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
	"github.com/googleapis/gax-go/v2"
)

// ErrChecksum means a payload failed the service-provided CRC32C check.
var ErrChecksum = errors.New("fastconf/secretmanager: payload checksum mismatch")

// API is implemented by *secretmanager.Client. The application owns the client.
type API interface {
	AccessSecretVersion(context.Context, *secretmanagerpb.AccessSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
}

// Config selects a full versions resource, including a number, latest or an
// alias. Codec is required because the resource name has no document extension.
type Config struct {
	Name, Codec string
	Priority    int // Defaults to contracts.PriorityKV.
}

// Provider loads the selected configuration using an application-owned client.
type Provider struct {
	cfg      Config
	client   API
	decoder  contracts.Decoder
	maxBytes int64
}

// Option configures a provider before registration.
type Option func(*Provider)

// WithMaxBodyBytes bounds decoded input, after SDK buffering. Non-positive
// values restore the default 4 MiB. Service limits may be smaller.
func WithMaxBodyBytes(n int64) Option {
	return func(p *Provider) {
		if n <= 0 {
			n = contracts.DefaultMaxBodyBytes
		}
		p.maxBytes = n
	}
}

// New validates the selection and borrows the client without changing it.
// The application owns the client and configures authentication and retries.
func New(cfg Config, client API, opts ...Option) (*Provider, error) {
	parts := strings.Split(cfg.Name, "/")
	var valid bool
	switch len(parts) {
	case 6: // Global secret version.
		valid = parts[0] == "projects" && parts[2] == "secrets" && parts[4] == "versions"
	case 8: // Regional secret version.
		valid = parts[0] == "projects" && parts[2] == "locations" && parts[4] == "secrets" && parts[6] == "versions"
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#") {
			valid = false
			break
		}
	}
	if client == nil || !valid || cfg.Codec == "" {
		return nil, errors.New("fastconf/secretmanager: client, version resource and codec are required")
	}

	decoder, err := codec.For(cfg.Codec)
	if err != nil {
		return nil, err
	}
	if cfg.Priority == 0 {
		cfg.Priority = contracts.PriorityKV
	}

	p := &Provider{cfg: cfg, client: client, decoder: decoder, maxBytes: contracts.DefaultMaxBodyBytes}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return "secretmanager:" + p.cfg.Name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.cfg.Priority}
}

// Load accesses the version on every refresh, including pinned versions, so a
// disabled/destroyed version cannot be masked by a local cache. Revision is the
// resolved version resource from the response. Secrets are never placed in errors.
// https://cloud.google.com/secret-manager/docs/data-integrity
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Snapshot{}, err
	}

	out, err := p.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: p.cfg.Name})
	if err != nil {
		return contracts.Snapshot{}, providerutil.SafeError("fastconf/secretmanager: "+"access"+" failed", err)
	}
	if out.Payload == nil || out.Name == "" {
		return contracts.Snapshot{}, errors.New("fastconf/secretmanager: missing payload or resolved version")
	}

	data := out.Payload.Data
	if int64(len(data)) > p.maxBytes {
		return contracts.Snapshot{}, contracts.ErrTooLarge
	}
	if out.Payload.DataCrc32C != nil && int64(crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))) != *out.Payload.DataCrc32C {
		return contracts.Snapshot{}, ErrChecksum
	}

	tree, err := p.decoder.Decode(data)
	if err != nil {
		return contracts.Snapshot{}, providerutil.SafeError("fastconf/secretmanager: "+"decode"+" failed", err)
	}
	return contracts.Snapshot{Map: tree, Revision: out.Name}, nil
}

var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
var _ API = (*sdk.Client)(nil)
