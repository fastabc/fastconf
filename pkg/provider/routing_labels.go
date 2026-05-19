package provider

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// RoutingLabelOptions configures the higher-level routing-label provider.
// Unlike LabelOptions / DottedLabelOptions, this preset understands common
// routing DSL shapes in addition to dotted-key expansion:
//
//   - typed scalar leaves ("true", "8080", "1.5");
//   - comma-delimited lists;
//   - indexed siblings such as domains[0].main → a promoted []any under
//     domains;
//   - optional whole-set gating via EnableGate.
//
// The zero value is intentionally provider-neutral: no prefix filter, no gate,
// and original key casing is preserved. Callers integrating a provider with
// stronger conventions can opt into the extra normalization knobs explicitly.
type RoutingLabelOptions struct {
	// Name overrides the default provider name.
	Name string
	// Priority sets the merge priority. Defaults to PriorityStatic (10).
	Priority int

	// Prefix restricts expansion to matching labels when non-empty.
	Prefix string
	// StripPrefix removes Prefix from each key before expansion.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".".
	Separator string
	// Separators is the ordered delimiter list. It takes precedence over Separator.
	Separators []string

	// EnableGate skips the whole label set when present and not truthy.
	EnableGate string

	// ListSeparator splits list-valued leaves. Empty falls back to ",".
	ListSeparator string
	// NoListSplit keeps list-looking values as one scalar leaf.
	NoListSplit bool
	// KeepRawSuffixes marks key suffixes that must remain raw strings.
	KeepRawSuffixes []string

	// Raw disables scalar coercion and list splitting.
	Raw bool
	// LowercaseKeys lowercases the full input key before filtering and expansion.
	LowercaseKeys bool
}

// RoutingLabelProvider injects routing-DSL labels as one configuration layer.
// It is read-only and does not watch its upstream source.
type RoutingLabelProvider struct {
	labels any
	opts   RoutingLabelOptions
}

// NewRoutingLabels constructs a routing-aware provider from []string labels in
// "key=value" form.
func NewRoutingLabels(labels []string, opts RoutingLabelOptions) *RoutingLabelProvider {
	return newRoutingLabelProvider(labels, opts)
}

// NewRoutingLabelMap is the map[string]string variant of NewRoutingLabels.
func NewRoutingLabelMap(labels map[string]string, opts RoutingLabelOptions) *RoutingLabelProvider {
	return newRoutingLabelProvider(labels, opts)
}

func newRoutingLabelProvider(labels any, opts RoutingLabelOptions) *RoutingLabelProvider {
	if opts.Priority == 0 {
		opts.Priority = contracts.PriorityStatic
	}
	if opts.Name == "" {
		opts.Name = "labels:routing"
		if opts.Prefix != "" {
			opts.Name += ":" + opts.Prefix
		}
	}
	return &RoutingLabelProvider{labels: labels, opts: opts}
}

// Name implements Provider.
func (p *RoutingLabelProvider) Name() string { return p.opts.Name }

// Priority implements Provider.
func (p *RoutingLabelProvider) Priority() int { return p.opts.Priority }

// Load implements Provider.
func (p *RoutingLabelProvider) Load(_ context.Context) (map[string]any, error) {
	pairs := collectRoutingLabelPairs(p.labels, p.opts.LowercaseKeys)
	if routingGateBlocks(pairs, normalizeRoutingKey(p.opts.EnableGate, p.opts.LowercaseKeys)) {
		return map[string]any{}, nil
	}

	tree := mappath.ExpandLabels(routingPairsAsList(pairs), mappath.LabelOptions{
		Prefix:      normalizeRoutingKey(p.opts.Prefix, p.opts.LowercaseKeys),
		StripPrefix: p.opts.StripPrefix,
		Separator:   p.opts.Separator,
		Separators:  p.opts.Separators,
	})
	transformRoutingTree(tree, nil, p.opts)
	return tree, nil
}

// Watch implements Provider. Routing labels are static after registration;
// callers with a live upstream should trigger Manager.Reload(ctx) themselves.
func (p *RoutingLabelProvider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}
