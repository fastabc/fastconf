package cliflag

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
)

// CLIProvider exposes explicitly set flags at PriorityCLI. Exclude parser defaults so they cannot
// override configured file or environment values.
type CLIProvider struct {
	data     map[string]any
	priority int
}

// NewCLI wraps explicitly set flags at [contracts.PriorityCLI]. Use FromStdFlag or
// integrations/cli/pflag.FromChanged to exclude defaults.
func NewCLI(data map[string]any) *CLIProvider {
	if data == nil {
		data = map[string]any{}
	}
	return &CLIProvider{data: data, priority: contracts.PriorityCLI}
}

// WithPriority overrides the default priority.
func (p *CLIProvider) WithPriority(prio int) *CLIProvider { p.priority = prio; return p }

// Name implements Provider.
func (p *CLIProvider) Name() string { return "cli" }

// Describe implements contracts.Describer.
func (p *CLIProvider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.priority}
}

// Load implements contracts.Provider.
func (p *CLIProvider) Load(ctx context.Context) (contracts.Snapshot, error) {
	m, err := p.loadMap(ctx)
	return contracts.Snapshot{Map: m}, err
}

func (p *CLIProvider) loadMap(_ context.Context) (map[string]any, error) { return p.data, nil }

// Watch implements Provider. CLI is fundamentally static for the process lifetime.
func (p *CLIProvider) Watch(_ context.Context, _ string) (<-chan contracts.Event, error) {
	return nil, nil
}
