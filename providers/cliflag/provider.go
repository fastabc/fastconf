package cliflag

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
)

// Provider exposes explicitly set flags at PriorityCLI. Exclude parser defaults so they cannot
// override configured file or environment values.
type Provider struct {
	data     map[string]any
	priority int
}

// New wraps explicitly set flags at [contracts.PriorityCLI]. Use cmd/internal/cli.ChangedValues or
// integrations/cli/pflag.FromChanged to exclude defaults.
func New(data map[string]any) *Provider {
	if data == nil {
		data = map[string]any{}
	}
	return &Provider{data: data, priority: contracts.PriorityCLI}
}

// WithPriority overrides the default priority.
func (p *Provider) WithPriority(prio int) *Provider { p.priority = prio; return p }

// Name implements Provider.
func (p *Provider) Name() string { return "cli" }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load implements contracts.Provider.
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.loadMap(ctx)
	return contracts.Snapshot{Map: m}, err
}

func (p *Provider) loadMap(_ context.Context) (map[string]any, error) { return p.data, nil }
