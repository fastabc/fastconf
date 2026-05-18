package provider

import (
	"context"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

// LabelOptions configures a LabelProvider. Mirrors mappath.LabelOptions so
// callers do not need to import two packages.
type LabelOptions struct {
	// Name overrides the default Provider name (otherwise "labels:<prefix>"
	// or "labels:" when prefix is empty).
	Name string
	// Priority sets the merge priority. Defaults to PriorityStatic (10):
	// labels have no inherent precedence, so callers integrating with a
	// Kubernetes controller should pass PriorityK8s explicitly, while
	// startup override use cases may choose PriorityCLI.
	Priority int
	// Prefix, when non-empty, restricts expansion to labels whose key starts
	// with this prefix (e.g. "routing.").
	Prefix string
	// StripPrefix removes Prefix from each key before expansion.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".". Use
	// Separators (plural) when multiple delimiters are in play (e.g.
	// K8s recommended labels with both "/" and ".").
	Separator string
	// Separators is the ordered list of delimiters applied to each key,
	// left to right. Pass {"/", "."} to make K8s-style
	// "app.kubernetes.io/name" decompose into ["app","kubernetes","io","name"].
	// When set, takes precedence over Separator.
	Separators []string
	// Coerce, when true, converts "true"/"false"/int/float strings into typed
	// values. Default false: values are kept verbatim, matching common
	// object-metadata and dotted-label inputs.
	Coerce bool
}

// DottedLabelOptions names the intent of labels that are deliberately used as
// dotted application configuration keys.
type DottedLabelOptions = LabelOptions

// LabelProvider injects a flat list (or map) of labels as a single
// configuration layer. NewLabels / NewLabelMap are low-level primitives for
// callers that already know how their label keys should be interpreted. Prefer
// NewDottedLabels / NewDottedLabelMap when the labels intentionally encode
// dotted application configuration. Use NewRoutingLabels / NewRoutingLabelMap
// when the source also carries routing-DSL value semantics such as typed leaves,
// comma lists, or indexed siblings.
//
// LabelProvider is read-only and does not implement Watch; pair it with a
// fastconf.Reload(ctx) call when the upstream label set changes.
type LabelProvider struct {
	labels any
	opts   LabelOptions
}

// NewLabels constructs a LabelProvider from a list of "key=value" strings,
// matching the Compose / docker CLI --label form.
func NewLabels(labels []string, opts LabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

// NewLabelMap constructs a LabelProvider from a key→value map, matching the
// Docker engine API / K8s annotation form.
func NewLabelMap(labels map[string]string, opts LabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

// NewDottedLabels constructs a LabelProvider from labels that intentionally
// encode dotted application configuration keys.
func NewDottedLabels(labels []string, opts DottedLabelOptions) *LabelProvider {
	return newLabelProvider(labels, opts)
}

// NewDottedLabelMap is the map[string]string variant for labels that
// intentionally encode dotted application configuration keys.
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

// Name implements Provider.
func (p *LabelProvider) Name() string { return p.opts.Name }

// Priority implements Provider.
func (p *LabelProvider) Priority() int { return p.opts.Priority }

// Load implements Provider.
func (p *LabelProvider) Load(_ context.Context) (map[string]any, error) {
	return mappath.ExpandLabels(p.labels, mappath.LabelOptions{
		Prefix:      p.opts.Prefix,
		StripPrefix: p.opts.StripPrefix,
		Separator:   p.opts.Separator,
		Separators:  p.opts.Separators,
		Coerce:      p.opts.Coerce,
	}), nil
}

// Watch implements Provider. Labels are static after registration; users who
// need live updates should call Manager.Reload(ctx) when the upstream label
// set changes.
func (p *LabelProvider) Watch(_ context.Context) (<-chan contracts.Event, error) { return nil, nil }
