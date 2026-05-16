//go:build !no_provider_s3

// Package s3 is a first-party AWS S3 provider for FastConf.
//
// The provider performs a single S3 GetObject on Load and decodes the
// response body using a FastConf codec resolved from the object key's
// extension (or the explicit Config.Codec when supplied). It is
// intentionally load-only: S3 has no native push notification model,
// and FastConf's pattern for change-driven reloads is to compose a
// dedicated watch provider (see providers/s3events) rather than hide
// polling inside this module. Watch returns (nil, nil).
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
// Credentials are explicit: callers supply AccessKey / SecretKey (and
// an optional STS Token). No ambient AWS_PROFILE or IMDS fallback —
// FastConf prefers configurations whose secret material is obvious
// from the call site. Pair with WithSecretResolver to load the
// credentials from a vault at Manager construction time.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/decoder"
)

// maxBodyBytes is the hard upper bound on an S3 object size accepted
// by Load. Configuration files larger than 10 MB almost certainly mean
// the wrong key was supplied; refusing them avoids OOM surprises on
// small Manager hosts.
const maxBodyBytes = 10 * 1024 * 1024

// Config holds all S3 provider settings. All required fields are
// validated at construction time by New; misconfiguration becomes a
// hard error rather than a deferred fetch failure.
type Config struct {
	Region    string // AWS region, e.g. "us-east-1" (required)
	Bucket    string // S3 bucket name (required)
	Key       string // Object key / path within bucket (required)
	AccessKey string // AWS access key ID (required)
	SecretKey string // AWS secret access key (required)
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

// Provider implements contracts.Provider. It is load-only (Watch
// returns (nil, nil)) but caches the last ETag and decoded body so
// repeated Load() calls only pay one HEAD-equivalent round-trip when
// the object has not changed.
type Provider struct {
	name      string
	bucket    string
	key       string
	versionID string
	priority  int
	codec     contracts.Codec
	client    API

	mu       sync.Mutex
	etag     string
	lastBody map[string]any
	loaded   bool
}

// New constructs an S3-backed provider. It validates Config, builds
// an aws-sdk-go-v2 S3 client with static credentials, and resolves the
// codec from Config.Codec (preferred) or the Key extension.
//
// Returns an error if a required field is missing or the codec cannot
// be determined.
func New(cfg Config) (*Provider, error) {
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
	return newProvider(cfg, client)
}

// NewWithClient is the test-friendly constructor: it accepts a
// pre-built API implementation instead of constructing a real AWS
// client. Production callers should use New.
func NewWithClient(cfg Config, client API) (*Provider, error) {
	if client == nil {
		return nil, errors.New("fastconf/s3: client is required")
	}
	return newProvider(cfg, client)
}

// newProvider validates the bucket/key/codec invariants shared by New
// and NewWithClient and assembles the Provider value. Credential
// validation is left to the public constructors because NewWithClient
// callers may legitimately supply an unauthenticated test client.
func newProvider(cfg Config, client API) (*Provider, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("fastconf/s3: bucket is required")
	}
	if cfg.Key == "" {
		return nil, errors.New("fastconf/s3: key is required")
	}
	codecName := cfg.Codec
	if codecName == "" {
		codecName = decoder.LookupExt(filepath.Ext(cfg.Key))
	}
	if codecName == "" {
		return nil, fmt.Errorf("fastconf/s3: cannot infer codec from key %q; set Config.Codec explicitly", cfg.Key)
	}
	codec, err := decoder.For(codecName)
	if err != nil {
		return nil, fmt.Errorf("fastconf/s3: %w", err)
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = contracts.PriorityKV
	}
	return &Provider{
		name:      fmt.Sprintf("s3://%s/%s", cfg.Bucket, cfg.Key),
		bucket:    cfg.Bucket,
		key:       cfg.Key,
		versionID: cfg.VersionID,
		priority:  priority,
		codec:     codec,
		client:    client,
	}, nil
}

// Name implements contracts.Provider. The returned identifier
// (s3://bucket/key) is stable across runs and unique within a Manager.
func (p *Provider) Name() string { return p.name }

// Priority implements contracts.Provider.
func (p *Provider) Priority() int { return p.priority }

// Load fetches the object and decodes the body. On the second and
// subsequent calls it sends If-None-Match with the cached ETag; a 304
// response short-circuits the decode and returns the cached map. The
// returned map is always a fresh shallow copy so the caller can mutate
// it without poisoning the cache.
func (p *Provider) Load(ctx context.Context) (map[string]any, error) {
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
			return cloneMap(p.lastBody), nil
		}
		return nil, fmt.Errorf("fastconf/s3: get %s: %w", p.name, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fastconf/s3: read %s: %w", p.name, err)
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, fmt.Errorf("fastconf/s3: object %s exceeds %d byte limit", p.name, maxBodyBytes)
	}
	m, derr := p.codec.Decode(body)
	if derr != nil {
		return nil, fmt.Errorf("fastconf/s3: decode %s: %w", p.name, derr)
	}
	p.mu.Lock()
	p.etag = aws.ToString(out.ETag)
	p.lastBody = m
	p.loaded = true
	p.mu.Unlock()
	return cloneMap(m), nil
}

// Watch implements contracts.Provider. The S3 provider is load-only;
// for change-driven reloads, pair with providers/s3events (S3 →
// EventBridge → SQS) which translates object-mutation events into
// contracts.Event values that trigger Manager.Reload.
func (p *Provider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
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

// cloneMap returns a shallow copy of m so callers can mutate the
// result without invalidating the cached body.
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// FromURL parses an s3:// (or s3+http(s) for non-AWS endpoints) URL
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

// Compile-time interface assertion.
var _ contracts.Provider = (*Provider)(nil)
