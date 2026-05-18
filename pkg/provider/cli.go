package provider

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
)

// CLIProvider exposes a pre-parsed map (typically built by the user from
// command-line flags) as the highest-priority static provider. Users wire it
// up in main() after their flag parsing completes — fastconf does not own the
// flag set. The map should contain only explicitly provided flags; parser
// defaults belong in lower-priority layers or they will override file config
// even when the user never typed the flag.
type CLIProvider struct {
	data     map[string]any
	priority int
}

// NewCLI wraps a map as the CLI layer. Prefer passing only explicitly changed
// flags; NewCLIChanged is a semantic alias when you want that intent visible
// at the call site.
func NewCLI(data map[string]any) *CLIProvider {
	if data == nil {
		data = map[string]any{}
	}
	return &CLIProvider{data: data, priority: contracts.PriorityCLI}
}

// NewCLIChanged wraps an explicit-override map as the CLI layer. It behaves
// exactly like NewCLI; the name exists to make "changed flags only" obvious
// at call sites.
func NewCLIChanged(data map[string]any) *CLIProvider { return NewCLI(data) }

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
