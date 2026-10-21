// Package parameterstore loads AWS Systems Manager Parameter Store hierarchies.
// Values remain strings; slash-separated names below Path become nested keys.
// The provider is load-only. The caller owns the SDK client and its credentials.
package parameterstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fastabc/fastconf/contracts"
)

// API is implemented by *ssm.Client and test clients.
type API interface {
	GetParametersByPath(context.Context, *ssm.GetParametersByPathInput, ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

// Config selects a hierarchy. Decrypt explicitly requests SecureString decryption
// and requires the corresponding KMS permissions. Priority defaults to PriorityKV.
type Config struct {
	Path     string
	Decrypt  bool
	Priority int
}

// Provider loads all pages atomically, rejecting conflicting leaf/branch names.
type Provider struct {
	client       API
	path         string
	decrypt      bool
	priority     int
	maxBodyBytes int64
}

// Option configures a provider before registration.
type Option func(*Provider)

// WithMaxBodyBytes bounds the aggregate parameter names and values. Non-positive
// values restore the default 4 MiB limit.
func WithMaxBodyBytes(n int64) Option {
	return func(p *Provider) {
		if n <= 0 {
			n = contracts.DefaultMaxBodyBytes
		}
		p.maxBodyBytes = n
	}
}

// New uses a configured SDK client. Path must be absolute; a trailing slash is
// accepted. Reads are recursive. The provider does not discover credentials.
func New(cfg Config, client API, opts ...Option) (*Provider, error) {
	if client == nil {
		return nil, errors.New("fastconf/parameterstore: client is required")
	}
	root := strings.TrimRight(cfg.Path, "/")
	if root == "" || !strings.HasPrefix(root, "/") {
		return nil, errors.New("fastconf/parameterstore: non-root absolute path is required")
	}
	for _, part := range strings.Split(root[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return nil, errors.New("fastconf/parameterstore: invalid path")
		}
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = contracts.PriorityKV
	}
	p := &Provider{client: client, path: root, decrypt: cfg.Decrypt, priority: priority, maxBodyBytes: contracts.DefaultMaxBodyBytes}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return "ssm:" + p.path }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load fetches all pages before publishing a snapshot. Revision is a stable hash
// of names, values and parameter versions, independent of page ordering.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	type parameter struct {
		value   string
		version int64
	}
	values := map[string]parameter{}
	var total int64
	prefix := p.path + "/"
	recursive := true
	input := &ssm.GetParametersByPathInput{Path: &p.path, Recursive: &recursive, WithDecryption: &p.decrypt}
	tokens := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return contracts.Snapshot{}, err
		}
		page, err := p.client.GetParametersByPath(ctx, input)
		if err != nil {
			return contracts.Snapshot{}, fmt.Errorf("%s: load: %w", p.Name(), err)
		}
		for _, item := range page.Parameters {
			if item.Name == nil || item.Value == nil || !strings.HasPrefix(*item.Name, prefix) {
				return contracts.Snapshot{}, fmt.Errorf("%s: invalid parameter in response", p.Name())
			}
			name := strings.TrimPrefix(*item.Name, prefix)
			if _, exists := values[name]; exists {
				return contracts.Snapshot{}, fmt.Errorf("%s: duplicate parameter %q", p.Name(), name)
			}
			total += int64(len(*item.Name)) + int64(len(*item.Value))
			if total > p.maxBodyBytes {
				return contracts.Snapshot{}, fmt.Errorf("%s: %w: limit=%d bytes", p.Name(), contracts.ErrTooLarge, p.maxBodyBytes)
			}
			values[name] = parameter{value: *item.Value, version: item.Version}
		}
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		if tokens[*page.NextToken] {
			return contracts.Snapshot{}, fmt.Errorf("%s: repeated pagination token", p.Name())
		}
		tokens[*page.NextToken] = true
		input.NextToken = page.NextToken
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	data := map[string]any{}
	for _, name := range names {
		item := values[name]
		if err := insert(data, strings.Split(name, "/"), item.value); err != nil {
			return contracts.Snapshot{}, fmt.Errorf("%s: parameter %q: %w", p.Name(), name, err)
		}
		_, _ = fmt.Fprintf(hash, "%q:%q:%d;", name, item.value, item.version)
	}
	return contracts.Snapshot{Map: data, Revision: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}

func insert(root map[string]any, keys []string, value string) error {
	for i, key := range keys {
		if key == "" || key == "." || key == ".." {
			return errors.New("invalid key segment")
		}
		if i == len(keys)-1 {
			if _, exists := root[key]; exists {
				return errors.New("conflicting key")
			}
			root[key] = value
			return nil
		}
		next, exists := root[key]
		if !exists {
			next = map[string]any{}
			root[key] = next
		}
		branch, ok := next.(map[string]any)
		if !ok {
			return errors.New("conflicting leaf and branch")
		}
		root = branch
	}
	return nil
}

var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
