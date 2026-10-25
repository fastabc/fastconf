// Package s3 is a first-party AWS S3 provider for FastConf.
//
// The provider performs a single S3 GetObject on Load and decodes the
// response body using a FastConf codec resolved from the object key's
// extension (or the explicit Config.Codec when supplied). It is
// load-only and does not implement contracts.Watcher. For change-driven reloads,
// use integrations/notify/sns with a synchronous Manager.Reload callback and
// periodic reloads to recover from missed notifications.
//
// # ETag short-circuit
//
// After the first successful Load, every subsequent Load sends the
// remembered ETag in If-None-Match. When the object has not changed,
// AWS returns 304 Not Modified and the provider serves the cached
// decoded map without re-decoding the body. This makes repeated
// Reload() calls cheap (one round-trip, no decode) and matches the
// no-spurious-reload contract enforced by providers/http.
//
// # Credentials
//
// New accepts explicit AccessKey / SecretKey and an optional STS Token.
// NewWithClient borrows an application-configured SDK client, including clients
// using the AWS credential chain, workload identity or refreshing credentials.
// The application owns that client. Manager secret resolvers process loaded
// configuration; they do not initialize the provider's SDK credentials.
package s3

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

const defaultMaxBodyBytes = 10 * 1024 * 1024

// Config holds all S3 provider settings. All required fields are
// validated at construction time by New; misconfiguration becomes a
// hard error rather than a deferred fetch failure.
type Config struct {
	Region    string // AWS region, e.g. "us-east-1" (required by New)
	Bucket    string // S3 bucket name (required)
	Key       string // Object key / path within bucket (required)
	AccessKey string // AWS access key ID (required by New)
	SecretKey string // AWS secret access key (required by New)
	Token     string // STS session token — optional, leave empty if not using STS
	Codec     string // Codec name; auto-inferred from Key extension if empty
	Priority  int    // Provider priority; default: contracts.PriorityKV
	Endpoint  string // Custom endpoint URL for non-AWS S3-compatible stores (MinIO, LocalStack)
	PathStyle bool   // Use path-style addressing (required for most non-AWS endpoints)
	VersionID string // Pin a specific object version; empty means "current"
}

// Credentials carries the three AWS secret-material fields. It exists
// as a separate type so that helpers like FromURL can take credentials
// without encoding them in the URL itself.
type Credentials struct {
	AccessKey string
	SecretKey string
	Token     string
}

// API is the minimal subset of the AWS S3 client needed by Provider.
// Tests substitute an in-memory implementation; production code uses
// *awss3.Client returned by aws-sdk-go-v2/service/s3.
type API interface {
	GetObject(ctx context.Context, in *awss3.GetObjectInput, opts ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
}

// Provider implements contracts.Provider without contracts.Watcher.
// It caches the last ETag and decoded body so
// repeated Load() calls only pay one HEAD-equivalent round-trip when
// the object has not changed.
// For notifications, use integrations/notify/sns and periodic reloads.
// The legacy s3events watcher remains available for best-effort invalidation.
type Provider struct {
	name         string
	bucket       string
	key          string
	versionID    string
	priority     int
	codec        contracts.Decoder
	client       API
	maxBodyBytes int64

	mu       sync.Mutex
	etag     string
	lastBody map[string]any
	loaded   bool
}

// Option configures an S3 provider during construction.
type Option func(*Provider)

// WithMaxBodyBytes limits the object body accepted by Load. Non-positive values
// restore the 10 MiB default. Oversized bodies return contracts.ErrTooLarge.
func WithMaxBodyBytes(n int64) Option {
	return func(p *Provider) {
		p.maxBodyBytes = n
		if n <= 0 {
			p.maxBodyBytes = defaultMaxBodyBytes
		}
	}
}

// New constructs an S3-backed provider. It validates Config, builds
// an aws-sdk-go-v2 S3 client with static credentials, and resolves the
// codec from Config.Codec (preferred) or the Key extension.
//
// Returns an error if a required field is missing or the codec cannot
// be determined.
func New(cfg Config, opts ...Option) (*Provider, error) {
	if cfg.Region == "" {
		return nil, errors.New("fastconf/s3: region is required")
	}
	if cfg.AccessKey == "" {
		return nil, errors.New("fastconf/s3: access key is required")
	}
	if cfg.SecretKey == "" {
		return nil, errors.New("fastconf/s3: secret key is required")
	}
	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, cfg.Token,
		),
	}
	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
	})
	return newProvider(cfg, client, opts...)
}

// NewWithClient borrows an application-configured client without changing it.
// Configure workload identity, refreshing credentials, transport limits and
// retries on the client before construction. The caller owns its lifecycle.
// Config's Region, credentials, Endpoint and PathStyle are not used here.
func NewWithClient(cfg Config, client API, opts ...Option) (*Provider, error) {
	if client == nil {
		return nil, errors.New("fastconf/s3: client is required")
	}
	return newProvider(cfg, client, opts...)
}

// newProvider validates the bucket/key/codec invariants shared by New
// and NewWithClient and assembles the Provider value. Credential
// validation is left to the public constructors because NewWithClient
// callers may legitimately supply an unauthenticated test client.
func newProvider(cfg Config, client API, opts ...Option) (*Provider, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("fastconf/s3: bucket is required")
	}
	if cfg.Key == "" {
		return nil, errors.New("fastconf/s3: key is required")
	}
	codecName := cfg.Codec
	if codecName == "" {
		codecName = codec.LookupExt(filepath.Ext(cfg.Key))
	}
	if codecName == "" {
		return nil, fmt.Errorf("fastconf/s3: cannot infer codec from key %q; set Config.Codec explicitly", cfg.Key)
	}
	dec, err := codec.For(codecName)
	if err != nil {
		return nil, fmt.Errorf("fastconf/s3: %w", err)
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = contracts.PriorityKV
	}
	p := &Provider{
		name:         fmt.Sprintf("s3://%s/%s", cfg.Bucket, cfg.Key),
		bucket:       cfg.Bucket,
		key:          cfg.Key,
		versionID:    cfg.VersionID,
		priority:     priority,
		codec:        dec,
		client:       client,
		maxBodyBytes: defaultMaxBodyBytes,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider. The returned identifier
// (s3://bucket/key) is stable across runs and unique within a Manager.
func (p *Provider) Name() string { return p.name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load fetches the object and decodes the body. On the second and
// subsequent calls it sends If-None-Match with the cached ETag; a 304
// response short-circuits the decode and returns the cached map. The
// returned map is always a deep copy so the caller can mutate
// it without poisoning the cache.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	p.mu.Lock()
	in := &awss3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(p.key),
	}
	if p.versionID != "" {
		in.VersionId = aws.String(p.versionID)
	}
	if p.loaded && p.etag != "" {
		in.IfNoneMatch = aws.String(p.etag)
	}
	p.mu.Unlock()

	out, err := p.client.GetObject(ctx, in)
	if err != nil {
		if isNotModified(err) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return contracts.Snapshot{Map: confmap.DeepClone(p.lastBody), Revision: p.etag}, nil
		}
		return contracts.Snapshot{}, &loadError{operation: "fastconf/s3: get " + p.name + " failed", err: err}
	}
	defer func() { _ = out.Body.Close() }()
	body, err := providerutil.ReadAllMax(out.Body, p.maxBodyBytes)
	if err != nil {
		return contracts.Snapshot{}, &loadError{operation: "fastconf/s3: read " + p.name + " failed", err: err}
	}

	m, derr := p.codec.Decode(body)
	if derr != nil {
		return contracts.Snapshot{}, &loadError{operation: "fastconf/s3: decode " + p.name + " failed", err: derr}
	}

	// Publish the ETag only after the complete body has decoded successfully.
	p.mu.Lock()
	p.etag = aws.ToString(out.ETag)
	p.lastBody = m
	p.loaded = true
	p.mu.Unlock()
	return contracts.Snapshot{Map: confmap.DeepClone(m), Revision: aws.ToString(out.ETag)}, nil
}

// isNotModified detects the 304 response the AWS SDK returns when an
// If-None-Match conditional GetObject finds the object unchanged.
// Both the aws-transport-http and smithy-transport-http response error
// types unwrap to *smithyhttp.ResponseError, so a single errors.As is
// sufficient.
func isNotModified(err error) bool {
	if err == nil {
		return false
	}
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode() == http.StatusNotModified
	}
	return false
}

// FromURL parses an s3:// URL (use the endpoint query for non-AWS stores)
// into a Config. Credentials are passed separately so secrets never
// appear in URLs that may be logged or persisted in YAML.
//
// Accepted schemes:
//
//	s3://<bucket>/<key>?region=<r>[&codec=<c>][&endpoint=<url>][&path_style=true][&version_id=<v>][&priority=<n>]
//
// Examples:
//
//	cfg, _ := s3.FromURL("s3://my-configs/prod/app.yaml?region=us-east-1", creds)
//	cfg, _ := s3.FromURL("s3://my-configs/cfg.json?region=us-east-1&endpoint=http://minio:9000&path_style=true", creds)
//
// The returned Config is suitable for direct use with New or for
// inclusion in a higher-level config that is itself loaded by
// FastConf — enabling "provider address as a config field" patterns.
func FromURL(rawurl string, creds Credentials) (Config, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return Config{}, fmt.Errorf("fastconf/s3: parse url: %w", err)
	}
	if u.Scheme != "s3" {
		return Config{}, fmt.Errorf("fastconf/s3: unsupported scheme %q (expected s3://)", u.Scheme)
	}
	if u.Host == "" {
		return Config{}, errors.New("fastconf/s3: bucket is required in url host")
	}
	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return Config{}, errors.New("fastconf/s3: key is required in url path")
	}
	q := u.Query()
	cfg := Config{
		Bucket:    u.Host,
		Key:       key,
		Region:    q.Get("region"),
		Codec:     q.Get("codec"),
		Endpoint:  q.Get("endpoint"),
		VersionID: q.Get("version_id"),
		AccessKey: creds.AccessKey,
		SecretKey: creds.SecretKey,
		Token:     creds.Token,
	}
	if v := q.Get("path_style"); v != "" {
		b, perr := strconv.ParseBool(v)
		if perr != nil {
			return Config{}, fmt.Errorf("fastconf/s3: path_style: %w", perr)
		}
		cfg.PathStyle = b
	}
	if v := q.Get("priority"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return Config{}, fmt.Errorf("fastconf/s3: priority: %w", perr)
		}
		cfg.Priority = n
	}
	return cfg, nil
}

// Decoder and transport errors may include document values or credentials.
// Preserve classification through Unwrap without displaying those details.
type loadError struct {
	operation string
	err       error
}

func (e *loadError) Error() string { return e.operation }
func (e *loadError) Unwrap() error { return e.err }

// Compile-time interface assertion.
var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
