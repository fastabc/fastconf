// Package gcs loads configuration documents from Google Cloud Storage.
// It is load-only; the caller configures, owns and closes the storage client.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"

	"cloud.google.com/go/storage"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// Config selects an object. Codec defaults to the key's extension; Priority
// defaults to contracts.PriorityKV.
type Config struct {
	Bucket   string
	Key      string
	Codec    string
	Priority int
}

// Provider caches successfully decoded object generations.
type Provider struct {
	client       *storage.Client
	bucket       string
	key          string
	codec        contracts.Decoder
	priority     int
	maxBodyBytes int64
	loadGate     chan struct{} // Serializes loads while allowing waiting callers to cancel.
	generation   int64
	data         map[string]any
}

// Option configures a provider before registration.
type Option func(*Provider)

// WithMaxBodyBytes bounds downloaded documents. Non-positive values restore
// the default 4 MiB limit.
func WithMaxBodyBytes(n int64) Option {
	return func(p *Provider) {
		if n <= 0 {
			n = contracts.DefaultMaxBodyBytes
		}
		p.maxBodyBytes = n
	}
}

// New uses an application-owned SDK client, allowing explicit credentials,
// workload identity or emulator settings without changing the provider.
func New(cfg Config, client *storage.Client, opts ...Option) (*Provider, error) {
	if client == nil {
		return nil, errors.New("fastconf/gcs: client is required")
	}
	if cfg.Bucket == "" || cfg.Key == "" {
		return nil, errors.New("fastconf/gcs: bucket and key are required")
	}

	name := cfg.Codec
	if name == "" {
		name = codec.LookupExt(path.Ext(cfg.Key))
	}
	dec, err := codec.For(name)
	if err != nil {
		return nil, fmt.Errorf("fastconf/gcs: codec for %q: %w", cfg.Key, err)
	}

	priority := cfg.Priority
	if priority == 0 {
		priority = contracts.PriorityKV
	}

	p := &Provider{
		client:       client,
		bucket:       cfg.Bucket,
		key:          cfg.Key,
		codec:        dec,
		priority:     priority,
		maxBodyBytes: contracts.DefaultMaxBodyBytes,
		loadGate:     make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return "gcs://" + p.bucket + "/" + p.key }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load checks object metadata and downloads only changed generations. The read
// is pinned to the observed generation so a concurrent replacement cannot mix
// the revision and document. Failed reads and decodes do not update the cache.
// Concurrent loads are serialized; waiting for a load honors ctx cancellation.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	select {
	case p.loadGate <- struct{}{}:
		defer func() { <-p.loadGate }()
	case <-ctx.Done():
		return contracts.Snapshot{}, ctx.Err()
	}

	// Cancellation may race with acquisition of an available gate.
	if err := ctx.Err(); err != nil {
		return contracts.Snapshot{}, err
	}

	object := p.client.Bucket(p.bucket).Object(p.key)
	attrs, err := object.Attrs(ctx)
	if err != nil {
		return contracts.Snapshot{}, providerutil.SafeError(p.Name()+": metadata failed", err)
	}
	if p.data != nil && attrs.Generation == p.generation {
		return contracts.Snapshot{Map: confmap.DeepClone(p.data), Revision: strconv.FormatInt(p.generation, 10)}, nil
	}

	reader, err := object.Generation(attrs.Generation).NewReader(ctx)
	if err != nil {
		return contracts.Snapshot{}, providerutil.SafeError(p.Name()+": read failed", err)
	}
	defer func() { _ = reader.Close() }()
	body, err := providerutil.ReadAllMax(reader, p.maxBodyBytes)
	if err != nil {
		return contracts.Snapshot{}, providerutil.SafeError(p.Name()+": read failed", err)
	}

	data, err := p.codec.Decode(body)
	if err != nil {
		return contracts.Snapshot{}, providerutil.SafeError(p.Name()+": decode failed", err)
	}

	p.generation, p.data = attrs.Generation, data
	return contracts.Snapshot{Map: confmap.DeepClone(data), Revision: strconv.FormatInt(p.generation, 10)}, nil
}

// Decoder and transport errors may include document values or credentials.
// Preserve classification through Unwrap without displaying those details.

var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
