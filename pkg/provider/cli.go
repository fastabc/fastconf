package provider

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
)

// CLIProvider exposes a pre-parsed map (typically built by the user from
// command-line flags) as the highest-priority static provider. Users wire it
// up in main() after their flag parsing completes — fastconf does not own the
// flag set.
type CLIProvider struct {
	data     map[string]any
	priority int
}

// NewCLI wraps a map as the CLI layer.
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

// Priority implements Provider.
func (p *CLIProvider) Priority() int { return p.priority }

// Load implements Provider.
func (p *CLIProvider) Load(_ context.Context) (map[string]any, error) { return p.data, nil }

// Watch implements Provider. CLI is fundamentally static for the process
// lifetime.
func (p *CLIProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }
