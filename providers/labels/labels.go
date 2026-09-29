// Package labels turns flat "key=value" labels (Compose / docker --label, Docker engine and K8s
// annotation maps) into one configuration layer.
package labels

import (
	"context"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
)

// Options configures a label provider.
type Options struct {
	// Name overrides the default provider name.
	Name string
	// Priority sets the merge priority. Defaults to PriorityStatic: labels
	// are a representation, not a deployment layer; raise it to K8s or CLI
	// precedence explicitly.
	Priority int
	// Prefix restricts expansion to matching labels when non-empty.
	Prefix string
	// StripPrefix removes Prefix from each key before expansion.
	StripPrefix bool
	// Separators is the ordered key delimiter list. Default {"."}.
	Separators []string
	// Coerce converts bool/int/float strings into typed values. Routing
	// mode coerces unless Routing.Raw is set.
	Coerce bool
	// Routing enables routing-DSL semantics when non-nil.
	Routing *Routing
}

// Routing enables the routing-label DSL on top of dotted expansion: typed scalar leaves ("true",
// "8080", "1.5"), comma-delimited lists, indexed siblings such as domains[0].main promoted to a
// []any (index at most MaxRoutingIndex), and optional whole-set gating. The zero value applies those with no gate and original key
// casing.
type Routing struct {
	// EnableGate skips the whole label set when present and not truthy.
	EnableGate string
	// ListSeparator splits list-valued leaves. Empty falls back to ",".
	ListSeparator string
	// NoListSplit keeps list-looking values as one scalar leaf.
	NoListSplit bool
	// KeepRawSuffixes marks key suffixes that must remain raw strings.
	// nil defaults to {".rule", "regexp"}; an empty slice disables it.
	KeepRawSuffixes []string
	// Raw disables scalar coercion and list splitting.
	Raw bool
	// LowercaseKeys lowercases the full input key before filtering and expansion.
	LowercaseKeys bool
}

// Provider injects labels as a single static configuration layer.
type Provider struct {
	labels any
	opts   Options
}

// New returns a provider for labels given as []string / []any of "key=value" entries, or as
// map[string]string / map[string]any.
func New(labels any, opts Options) *Provider {
	if opts.Priority == 0 {
		opts.Priority = contracts.PriorityStatic
	}
	if opts.Name == "" {
		switch {
		case opts.Routing == nil:
			opts.Name = "labels:" + opts.Prefix
		case opts.Prefix != "":
			opts.Name = "labels:routing:" + opts.Prefix
		default:
			opts.Name = "labels:routing"
		}
	}
	return &Provider{labels: labels, opts: opts}
}

// Name implements contracts.Provider.
func (p *Provider) Name() string { return p.opts.Name }

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.opts.Priority}
}

// Load implements contracts.Provider.
func (p *Provider) Load(context.Context) (contracts.Snapshot, error) {
	if p.opts.Routing != nil {
		m, err := p.loadRouting()
		return contracts.Snapshot{Map: m}, err
	}
	return contracts.Snapshot{Map: confmap.ExpandLabels(p.labels, confmap.LabelOptions{
		Prefix:      p.opts.Prefix,
		StripPrefix: p.opts.StripPrefix,
		Separators:  p.opts.Separators,
		Coerce:      p.opts.Coerce,
	})}, nil
}

func (p *Provider) loadRouting() (map[string]any, error) {
	r := p.opts.Routing
	pairs := collectRoutingLabelPairs(p.labels, r.LowercaseKeys)
	if routingGateBlocks(pairs, normalizeRoutingKey(r.EnableGate, r.LowercaseKeys)) {
		return map[string]any{}, nil
	}
	tree := confmap.ExpandLabels(routingPairsAsList(pairs), confmap.LabelOptions{
		Prefix:      normalizeRoutingKey(p.opts.Prefix, r.LowercaseKeys),
		StripPrefix: p.opts.StripPrefix,
		Separators:  p.opts.Separators,
	})
	if err := transformRoutingTree(tree, nil, r); err != nil {
		return nil, err
	}
	return tree, nil
}

// Watch returns (nil, nil): labels are static after registration; callers with a live upstream
// trigger Manager.Reload themselves.
func (p *Provider) Watch(context.Context, string) (<-chan contracts.Event, error) {
	return nil, nil
}
