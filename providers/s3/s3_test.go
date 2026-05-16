//go:build !no_provider_s3

package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/fastabc/fastconf/contracts"
	s3prov "github.com/fastabc/fastconf/providers/s3"
)

// fakeObject is one entry in fakeAPI's store. ETag is matched against
// GetObjectInput.IfNoneMatch to simulate 304 short-circuit; Version is
// matched against GetObjectInput.VersionId when non-empty.
type fakeObject struct {
	Body    []byte
	ETag    string
	Version string
}

// fakeAPI is an in-memory S3 client for tests. It tracks every
// GetObject call so assertions can verify bucket / key / IfNoneMatch /
// VersionId. Returning a synthetic *smithyhttp.ResponseError lets
// tests exercise the 304 short-circuit without going near AWS.
type fakeAPI struct {
	objects map[string]fakeObject // "bucket/key" → object
	err     error
	calls   int
	lastIn  *awss3.GetObjectInput
}

func (f *fakeAPI) GetObject(_ context.Context, in *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	f.calls++
	f.lastIn = in
	if f.err != nil {
		return nil, f.err
	}
	k := aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key)
	obj, ok := f.objects[k]
	if !ok {
		return nil, errors.New("not found: " + k)
	}
	if v := aws.ToString(in.VersionId); v != "" && obj.Version != "" && v != obj.Version {
		return nil, errors.New("version mismatch")
	}
	if reqETag := aws.ToString(in.IfNoneMatch); reqETag != "" && obj.ETag != "" && reqETag == obj.ETag {
		return nil, &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &nethttp.Response{StatusCode: nethttp.StatusNotModified}},
			Err:      errors.New("not modified"),
		}
	}
	out := &awss3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(obj.Body))}
	if obj.ETag != "" {
		out.ETag = aws.String(obj.ETag)
	}
	return out, nil
}

func TestNew_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  s3prov.Config
	}{
		{"missing region", s3prov.Config{Bucket: "b", Key: "k.yaml", AccessKey: "a", SecretKey: "s"}},
		{"missing bucket", s3prov.Config{Region: "us-east-1", Key: "k.yaml", AccessKey: "a", SecretKey: "s"}},
		{"missing key", s3prov.Config{Region: "us-east-1", Bucket: "b", AccessKey: "a", SecretKey: "s"}},
		{"missing access key", s3prov.Config{Region: "us-east-1", Bucket: "b", Key: "k.yaml", SecretKey: "s"}},
		{"missing secret key", s3prov.Config{Region: "us-east-1", Bucket: "b", Key: "k.yaml", AccessKey: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s3prov.New(tc.cfg); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestNew_CodecInferenceFromExt(t *testing.T) {
	cases := []struct {
		key  string
		want bool // construction succeeds?
	}{
		{"prod/app.yaml", true},
		{"prod/app.yml", true},
		{"prod/app.json", true},
		{"prod/app.toml", true},
		{"prod/app.hcl", false},   // no codec registered for hcl
		{"prod/app.noext", false}, // no extension
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			_, err := s3prov.New(s3prov.Config{
				Region:    "us-east-1",
				Bucket:    "b",
				Key:       tc.key,
				AccessKey: "AKIA",
				SecretKey: "secret",
			})
			if tc.want && err != nil {
				t.Errorf("expected success, got %v", err)
			}
			if !tc.want && err == nil {
				t.Error("expected error for unresolvable codec")
			}
		})
	}
}

func TestNew_ExplicitCodecOverridesExt(t *testing.T) {
	_, err := s3prov.New(s3prov.Config{
		Region:    "us-east-1",
		Bucket:    "b",
		Key:       "configs/latest",
		AccessKey: "AKIA",
		SecretKey: "secret",
		Codec:     "yaml",
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestProvider_LoadDecodesYAML(t *testing.T) {
	api := &fakeAPI{
		objects: map[string]fakeObject{
			"my-bucket/prod/app.yaml": {Body: []byte("server:\n  addr: :8080\n")},
		},
	}
	p, err := s3prov.NewWithClient(s3prov.Config{Bucket: "my-bucket", Key: "prod/app.yaml"}, api)
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv, ok := m["server"].(map[string]any)
	if !ok {
		t.Fatalf("server key missing or wrong shape: %v", m)
	}
	if srv["addr"] != ":8080" {
		t.Errorf("addr: %v", srv["addr"])
	}
	if api.calls != 1 {
		t.Errorf("expected 1 GetObject call, got %d", api.calls)
	}
}

func TestProvider_LoadDecodesJSON(t *testing.T) {
	api := &fakeAPI{objects: map[string]fakeObject{"b/k.json": {Body: []byte(`{"k":"v"}`)}}}
	p, err := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.json"}, api)
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m["k"] != "v" {
		t.Errorf("got %v", m)
	}
}

func TestProvider_LoadPropagatesGetError(t *testing.T) {
	api := &fakeAPI{err: errors.New("AccessDenied")}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.yaml"}, api)
	_, err := p.Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("expected AccessDenied error, got %v", err)
	}
}

func TestProvider_LoadRejectsOversizedBody(t *testing.T) {
	big := make([]byte, 10*1024*1024+1)
	api := &fakeAPI{objects: map[string]fakeObject{"b/k.yaml": {Body: big}}}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.yaml"}, api)
	if _, err := p.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestProvider_LoadDecodeError(t *testing.T) {
	api := &fakeAPI{objects: map[string]fakeObject{"b/k.json": {Body: []byte("not-json")}}}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.json"}, api)
	if _, err := p.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

// TestProvider_ETagShortCircuit verifies that a second Load reusing the
// same ETag short-circuits to the cached map, even when the underlying
// "object" body has changed to something that would no longer decode.
// That last detail is the strongest guarantee that the decode path was
// truly skipped on the 304 branch.
func TestProvider_ETagShortCircuit(t *testing.T) {
	api := &fakeAPI{
		objects: map[string]fakeObject{
			"b/cfg.yaml": {Body: []byte("k: v1"), ETag: `"abc"`},
		},
	}
	p, err := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "cfg.yaml"}, api)
	if err != nil {
		t.Fatal(err)
	}
	m1, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if m1["k"] != "v1" {
		t.Errorf("first Load decoded wrong: %v", m1)
	}
	// Swap the body to garbage that would fail decode. Same ETag → server
	// returns 304 → provider must serve the cached map.
	api.objects["b/cfg.yaml"] = fakeObject{Body: []byte("not-yaml: ::"), ETag: `"abc"`}
	m2, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if m2["k"] != "v1" {
		t.Errorf("expected cached v1, got %v", m2)
	}
	if api.calls != 2 {
		t.Errorf("expected 2 GetObject calls, got %d", api.calls)
	}
	// Second call must have carried If-None-Match.
	if got := aws.ToString(api.lastIn.IfNoneMatch); got != `"abc"` {
		t.Errorf("expected IfNoneMatch=\"abc\", got %q", got)
	}
}

// TestProvider_ETagRefresh verifies that when the server's ETag
// changes, the provider re-decodes the new body and updates the cache.
func TestProvider_ETagRefresh(t *testing.T) {
	api := &fakeAPI{
		objects: map[string]fakeObject{
			"b/cfg.yaml": {Body: []byte("k: v1"), ETag: `"v1"`},
		},
	}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "cfg.yaml"}, api)
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.objects["b/cfg.yaml"] = fakeObject{Body: []byte("k: v2"), ETag: `"v2"`}
	m, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if m["k"] != "v2" {
		t.Errorf("expected v2, got %v", m)
	}
}

// TestProvider_LoadReturnsFreshMap verifies that Load returns a copy so
// callers can mutate the map without poisoning the cache.
func TestProvider_LoadReturnsFreshMap(t *testing.T) {
	api := &fakeAPI{
		objects: map[string]fakeObject{
			"b/cfg.yaml": {Body: []byte("k: v"), ETag: `"e"`},
		},
	}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "cfg.yaml"}, api)
	m1, _ := p.Load(context.Background())
	m1["k"] = "MUTATED"
	m2, _ := p.Load(context.Background()) // hits 304 path
	if m2["k"] != "v" {
		t.Errorf("cache was poisoned: %v", m2)
	}
}

func TestProvider_VersionIDPassedThrough(t *testing.T) {
	api := &fakeAPI{
		objects: map[string]fakeObject{
			"b/cfg.yaml": {Body: []byte("k: v"), Version: "v42"},
		},
	}
	p, err := s3prov.NewWithClient(s3prov.Config{
		Bucket:    "b",
		Key:       "cfg.yaml",
		VersionID: "v42",
	}, api)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := aws.ToString(api.lastIn.VersionId); got != "v42" {
		t.Errorf("expected VersionId=v42, got %q", got)
	}
}

func TestProvider_NameAndPriority(t *testing.T) {
	api := &fakeAPI{}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "my-bucket", Key: "prod/app.yaml"}, api)
	if p.Name() != "s3://my-bucket/prod/app.yaml" {
		t.Errorf("Name: %s", p.Name())
	}
	if p.Priority() != contracts.PriorityKV {
		t.Errorf("Priority: %d", p.Priority())
	}

	p2, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.yaml", Priority: 99}, api)
	if p2.Priority() != 99 {
		t.Errorf("custom priority: %d", p2.Priority())
	}
}

func TestProvider_WatchReturnsNil(t *testing.T) {
	api := &fakeAPI{}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.yaml"}, api)
	ch, err := p.Watch(context.Background())
	if err != nil || ch != nil {
		t.Errorf("Watch: expected (nil, nil), got (%v, %v)", ch, err)
	}
}

func TestProvider_ImplementsContracts(t *testing.T) {
	api := &fakeAPI{}
	p, _ := s3prov.NewWithClient(s3prov.Config{Bucket: "b", Key: "k.yaml"}, api)
	var _ contracts.Provider = p
}

func TestNew_BuildsRealClient(t *testing.T) {
	p, err := s3prov.New(s3prov.Config{
		Region:    "us-east-1",
		Bucket:    "b",
		Key:       "k.yaml",
		AccessKey: "AKIA",
		SecretKey: "secret",
		Endpoint:  "http://localhost:9000",
		PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "s3://b/k.yaml" {
		t.Errorf("Name: %s", p.Name())
	}
}

func TestFromURL_Happy(t *testing.T) {
	cfg, err := s3prov.FromURL(
		"s3://my-configs/prod/app.yaml?region=us-east-1",
		s3prov.Credentials{AccessKey: "AKIA", SecretKey: "secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bucket != "my-configs" || cfg.Key != "prod/app.yaml" {
		t.Errorf("bucket/key: %+v", cfg)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region: %q", cfg.Region)
	}
	if cfg.AccessKey != "AKIA" || cfg.SecretKey != "secret" {
		t.Errorf("creds: %+v", cfg)
	}
}

func TestFromURL_AllQueryParams(t *testing.T) {
	cfg, err := s3prov.FromURL(
		"s3://b/path/to/cfg.json?region=eu-west-1&codec=yaml&endpoint=http://minio:9000&path_style=true&version_id=v42&priority=33",
		s3prov.Credentials{AccessKey: "k", SecretKey: "s", Token: "t"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Codec != "yaml" {
		t.Errorf("codec: %q", cfg.Codec)
	}
	if cfg.Endpoint != "http://minio:9000" {
		t.Errorf("endpoint: %q", cfg.Endpoint)
	}
	if !cfg.PathStyle {
		t.Errorf("path_style not parsed")
	}
	if cfg.VersionID != "v42" {
		t.Errorf("version_id: %q", cfg.VersionID)
	}
	if cfg.Priority != 33 {
		t.Errorf("priority: %d", cfg.Priority)
	}
	if cfg.Token != "t" {
		t.Errorf("token not threaded: %q", cfg.Token)
	}
}

func TestFromURL_Errors(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"wrong scheme", "https://bucket/key"},
		{"no host", "s3:///key"},
		{"no key", "s3://bucket"},
		{"bad path_style", "s3://b/k.yaml?path_style=maybe"},
		{"bad priority", "s3://b/k.yaml?priority=high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s3prov.FromURL(tc.url, s3prov.Credentials{}); err == nil {
				t.Errorf("expected error for %s", tc.url)
			}
		})
	}
}

func TestFromURL_RoundTripWithNew(t *testing.T) {
	// FromURL → New end-to-end happy path: ensures the parsed Config is
	// directly usable by New() (which fans out to the real AWS client).
	cfg, err := s3prov.FromURL(
		"s3://b/k.yaml?region=us-east-1",
		s3prov.Credentials{AccessKey: "AKIA", SecretKey: "secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s3prov.New(cfg)
	if err != nil {
		t.Fatalf("New from URL-derived cfg: %v", err)
	}
	if p.Name() != "s3://b/k.yaml" {
		t.Errorf("Name: %s", p.Name())
	}
}
