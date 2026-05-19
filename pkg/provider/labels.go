package provider

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// LabelOptions configures a LabelProvider. Mirrors mappath.LabelOptions so
// callers do not need to import two packages.
type LabelOptions struct {
	// Name overrides the default provider name.
	Name string
	// Priority sets the merge priority. Defaults to PriorityStatic.
	Priority int
	// Prefix restricts expansion to matching labels when non-empty.
	Prefix string
	// StripPrefix removes Prefix from each key before expansion.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".".
	Separator string
	// Separators is the ordered delimiter list. It takes precedence over Separator.
	Separators []string
	// Coerce converts bool/int/float strings into typed values.
	Coerce bool
}

// DottedLabelOptions marks labels that encode dotted application config keys.
type DottedLabelOptions = LabelOptions

// LabelProvider injects a flat list (or map) of labels as a single
// configuration layer. Use NewRoutingLabels when values need routing-DSL
// semantics such as comma lists or indexed siblings.
type LabelProvider struct {
	labels any
	opts   LabelOptions
}

// NewLabels constructs a LabelProvider from a list of "key=value" strings,
// matching the Compose / docker CLI --label form.
func NewLabels(labels []string, opts LabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

// NewLabelMap constructs a LabelProvider from a key/value map, matching the
// Docker engine API / K8s annotation form.
func NewLabelMap(labels map[string]string, opts LabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

// NewDottedLabels constructs a LabelProvider from labels that intentionally
// encode dotted application configuration keys.
func NewDottedLabels(labels []string, opts DottedLabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

// NewDottedLabelMap is the map variant for dotted application labels.
func NewDottedLabelMap(labels map[string]string, opts DottedLabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

func newLabelProvider(labels any, opts LabelOptions) *LabelProvider {
	// Labels are a representation, not a deployment-layer policy. Default to
	// the neutral static band and let callers opt into K8s / CLI precedence.
	if opts.Priority == 0 {
		opts.Priority = contracts.PriorityStatic
	}
	if opts.Name == "" {
		opts.Name = "labels:" + opts.Prefix
	}
	return &LabelProvider{labels: labels, opts: opts}
}

func (p *LabelProvider) Name() string { return p.opts.Name }

func (p *LabelProvider) Priority() int { return p.opts.Priority }

func (p *LabelProvider) Load(_ context.Context) (map[string]any, error) {
	return mappath.ExpandLabels(p.labels, mappath.LabelOptions{
		Prefix:      p.opts.Prefix,
		StripPrefix: p.opts.StripPrefix,
		Separator:   p.opts.Separator,
		Separators:  p.opts.Separators,
		Coerce:      p.opts.Coerce,
	}), nil
}

// Watch returns nil because label providers are static after registration.
func (p *LabelProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }
