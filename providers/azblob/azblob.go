// Package azblob loads configuration documents from Azure Blob Storage.
// It is load-only; authentication is configured on the application-owned client.
package azblob

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	sdk "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// API is implemented by *azblob.Client and test clients.
type API interface {
	URL() string
	DownloadStream(context.Context, string, string, *sdk.DownloadStreamOptions) (sdk.DownloadStreamResponse, error)
}

// Config selects a blob. Codec defaults to the key's extension; Priority defaults
// to contracts.PriorityKV.
type Config struct {
	Container string
	Key       string
	Codec     string
	Priority  int
}

// Provider uses conditional requests to cache successfully decoded blobs.
type Provider struct {
	name         string
	client       API
	container    string
	key          string
	codec        contracts.Decoder
	priority     int
	maxBodyBytes int64
	loadGate     chan struct{} // Serializes loads while allowing waiting callers to cancel.
	etag         azcore.ETag
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

// New uses a configured SDK client (token credential, shared key, SAS or emulator).
func New(cfg Config, client API, opts ...Option) (*Provider, error) {
	if client == nil {
		return nil, errors.New("fastconf/azblob: client is required")
	}
	if cfg.Container == "" || cfg.Key == "" {
		return nil, errors.New("fastconf/azblob: container and key are required")
	}

	account, err := url.Parse(client.URL())
	if err != nil || account.Host == "" || (account.Scheme != "http" && account.Scheme != "https") {
		return nil, errors.New("fastconf/azblob: invalid client service URL")
	}
	account.RawQuery, account.Fragment, account.User = "", "", nil

	name := cfg.Codec
	if name == "" {
		name = codec.LookupExt(path.Ext(cfg.Key))
	}
	dec, err := codec.For(name)
	if err != nil {
		return nil, fmt.Errorf("fastconf/azblob: codec for %q: %w", cfg.Key, err)
	}

	priority := cfg.Priority
	if priority == 0 {
		priority = contracts.PriorityKV
	}

	p := &Provider{
		name:         "azblob:" + strings.TrimRight(account.String(), "/") + "/" + cfg.Container + "/" + cfg.Key,
		client:       client,
		container:    cfg.Container,
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
func (p *Provider) Name() string { return p.name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load sends If-None-Match after a successful decode. A 304 returns a deep copy
// of the cached document. Rejected documents never seed the ETag cache.
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

	options := &sdk.DownloadStreamOptions{}
	if p.data != nil && p.etag != "" {
		options.AccessConditions = &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: &p.etag}}
	}
	out, err := p.client.DownloadStream(ctx, p.container, p.key, options)
	if err != nil {
		var response *azcore.ResponseError
		if p.data != nil && errors.As(err, &response) && response.StatusCode == http.StatusNotModified {
			return contracts.Snapshot{Map: confmap.DeepClone(p.data), Revision: string(p.etag)}, nil
		}
		return contracts.Snapshot{}, &loadError{operation: p.Name() + ": download failed", err: err}
	}
	// The Azure SDK translates HTTP 304 into a successful response with an
	// ErrorCode pointer and no Body, rather than returning ResponseError.
	if out.Body == nil {
		if out.ErrorCode != nil {
			if options.AccessConditions != nil {
				return contracts.Snapshot{Map: confmap.DeepClone(p.data), Revision: string(p.etag)}, nil
			}
			return contracts.Snapshot{}, fmt.Errorf("%s: download: %w", p.Name(), &azcore.ResponseError{StatusCode: http.StatusNotModified, ErrorCode: *out.ErrorCode})
		}
		return contracts.Snapshot{}, fmt.Errorf("%s: download returned no body", p.Name())
	}
	defer func() { _ = out.Body.Close() }()

	body, err := providerutil.ReadAllMax(out.Body, p.maxBodyBytes)
	if err != nil {
		return contracts.Snapshot{}, &loadError{operation: p.Name() + ": read failed", err: err}
	}

	data, err := p.codec.Decode(body)
	if err != nil {
		return contracts.Snapshot{}, &loadError{operation: p.Name() + ": decode failed", err: err}
	}

	p.etag = ""
	if out.ETag != nil {
		p.etag = *out.ETag
	}
	p.data = data
	return contracts.Snapshot{Map: confmap.DeepClone(data), Revision: string(p.etag)}, nil
}

// SDK transport errors can contain SAS URLs; decoder errors can contain data.
// Keep those details available to errors.Is/As without displaying them by default.
type loadError struct {
	operation string
	err       error
}

func (e *loadError) Error() string { return e.operation }
func (e *loadError) Unwrap() error { return e.err }

var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
