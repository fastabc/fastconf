// Package azappconfig loads an Azure App Configuration key/label selection.
// Values stay strings. Keys below Prefix are split on colons, preserving dots.
package azappconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	sdk "github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// API is implemented by *azappconfig.Client; the application owns its credentials.
type API interface {
	NewListSettingsPager(sdk.SettingSelector, *sdk.ListSettingsOptions) *runtime.Pager[sdk.ListSettingsPageResponse]
}

// Config selects one literal prefix and one exact label. Empty Label selects
// unlabelled settings, not every label. Name distinguishes multiple stores.
type Config struct {
	Name, Prefix, Label string
	Priority            int // Defaults to contracts.PriorityKV.
}

// Provider loads the selected configuration using an application-owned client.
type Provider struct {
	cfg      Config
	client   API
	maxBytes int64
}

// Option configures a provider before registration.
type Option func(*Provider)

// WithMaxBodyBytes bounds the aggregate selected key, label and value bytes.
// SDK pages are buffered before this check. Non-positive values restore 4 MiB.
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
	if client == nil {
		return nil, errors.New("fastconf/azappconfig: client is required")
	}
	if strings.ContainsRune(cfg.Prefix+cfg.Label, 0) {
		return nil, errors.New("fastconf/azappconfig: NUL is not a valid selector")
	}

	if cfg.Name == "" {
		cfg.Name = "azappconfig:" + cfg.Prefix + "?label=" + url.QueryEscape(cfg.Label)
	}
	if cfg.Priority == 0 {
		cfg.Priority = contracts.PriorityKV
	}

	p := &Provider{cfg: cfg, client: client, maxBytes: contracts.DefaultMaxBodyBytes}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.cfg.Name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.cfg.Priority}
}

// Load collects every page before returning a tree. This is atomic delivery to
// FastConf, not a transactional cloud snapshot. Feature flags and Key Vault
// references need explicit application handling and are rejected here.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Snapshot{}, err
	}

	key, label := escape(p.cfg.Prefix)+"*", escape(p.cfg.Label)
	// Azure uses NUL for an explicit unlabelled selection:
	// https://learn.microsoft.com/azure/azure-app-configuration/rest-api-key-value
	if label == "" {
		label = "\x00"
	}

	pager := p.client.NewListSettingsPager(sdk.SettingSelector{KeyFilter: &key, LabelFilter: &label}, nil)
	settings := map[string]sdk.Setting{}
	var total int64
	for pager.More() {
		if err := ctx.Err(); err != nil {
			return contracts.Snapshot{}, err
		}
		page, err := pager.NextPage(ctx)
		if err != nil {
			return contracts.Snapshot{}, providerutil.SafeError("fastconf/azappconfig: "+"list"+" failed", err)
		}
		for _, setting := range page.Settings {
			if setting.Key == nil || setting.Value == nil {
				return contracts.Snapshot{}, errors.New("fastconf/azappconfig: response missing key/value")
			}
			if !strings.HasPrefix(*setting.Key, p.cfg.Prefix) || value(setting.Label) != p.cfg.Label {
				return contracts.Snapshot{}, errors.New("fastconf/azappconfig: response outside selection")
			}
			contentType := strings.ToLower(value(setting.ContentType))
			if strings.HasPrefix(contentType, "application/vnd.microsoft.appconfig.") {
				return contracts.Snapshot{}, errors.New("fastconf/azappconfig: special content type is not supported")
			}
			total += int64(len(*setting.Key) + len(*setting.Value) + len(value(setting.Label)))
			if total > p.maxBytes {
				return contracts.Snapshot{}, contracts.ErrTooLarge
			}
			if _, exists := settings[*setting.Key]; exists {
				return contracts.Snapshot{}, errors.New("fastconf/azappconfig: duplicate key")
			}
			settings[*setting.Key] = setting
		}
	}

	// Page order must not affect the revision or key conflict detection.
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	data, hash := map[string]any{}, sha256.New()
	for _, key := range keys {
		setting := settings[key]
		if err := insert(data, strings.Split(strings.TrimPrefix(key, p.cfg.Prefix), ":"), *setting.Value); err != nil {
			return contracts.Snapshot{}, err
		}
		etag := ""
		if setting.ETag != nil {
			etag = string(*setting.ETag)
		}
		_, _ = fmt.Fprintf(hash, "%q:%q:%q:%q;", key, p.cfg.Label, etag, *setting.Value)
	}
	return contracts.Snapshot{Map: data, Revision: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}

func escape(s string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", ",", "\\,").Replace(s)
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func insert(root map[string]any, keys []string, value string) error {
	for i, key := range keys {
		if key == "" {
			return errors.New("fastconf/azappconfig: empty key segment")
		}
		if i == len(keys)-1 {
			if _, exists := root[key]; exists {
				return errors.New("fastconf/azappconfig: conflicting key")
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
			return errors.New("fastconf/azappconfig: conflicting leaf and branch")
		}
		root = branch
	}
	return nil
}

var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)
var _ API = (*sdk.Client)(nil)
