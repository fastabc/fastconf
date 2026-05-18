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
//
// # Footgun: do not pass every flag's current value
//
// Spreading every defined flag — including those whose value is still the
// default — into the map causes the default to silently override values
// already set in the YAML / env / KV layers. This is the same trap that
// spf13/viper's BindPFlag is known for; fastconf does not insulate you from
// it. Always feed only the flags whose Changed / IsSet bit is true.
//
//	// WRONG: defaults leak into CLI layer
//	all := map[string]any{}
//	cmd.Flags().VisitAll(func(f *pflag.Flag) {
//	    all[f.Name] = f.Value.String() // includes untouched defaults!
//	})
//	mgr.Add(provider.NewCLI(all)) // app.yaml: server.port=9090 → overridden by default 8080
//
//	// RIGHT: only flags the user explicitly typed
//	import cliflag "github.com/fastabc/fastconf/integrations/cli/pflag"
//	mgr.Add(provider.NewCLIChanged(cliflag.FromChanged(cmd.Flags())))
//
// See pkg/cliadapter and integrations/cli/pflag for ready-made helpers that
// extract the "changed" subset for stdlib flag and spf13/pflag respectively.
type CLIProvider struct {
	data     map[string]any
	priority int
}

// NewCLI wraps a map as the CLI layer. Prefer passing only explicitly changed
// flags; NewCLIChanged is a semantic alias when you want that intent visible
// at the call site. See the package-level footgun note on CLIProvider for the
// hazard of passing flag defaults.
func NewCLI(data map[string]any) *CLIProvider {
	if data == nil {
		data = map[string]any{}
	}
	return &CLIProvider{data: data, priority: contracts.PriorityCLI}
}

// NewCLIChanged wraps an explicit-override map as the CLI layer. It behaves
// exactly like NewCLI; the name exists to make "changed flags only" obvious
// at call sites and to pair cleanly with cliadapter.FromStdFlag /
// integrations/cli/pflag.FromChanged, which yield such a map.
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
