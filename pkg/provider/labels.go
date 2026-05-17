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
	// Priority sets the merge priority. Defaults to PriorityK8s, matching
	// the typical "metadata.labels / metadata.annotations from a K8s
	// controller" use case. Traefik / Docker engine label sources that
	// must beat env values should set PriorityCLI explicitly.
	Priority int
	// Prefix, when non-empty, restricts expansion to labels whose key starts
	// with this prefix (e.g. "traefik.").
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
	// values. Default false: values are kept verbatim (matches Traefik /
	// Compose label semantics).
	Coerce bool
}

// LabelProvider injects a flat list (or map) of Traefik / Docker / K8s style
// dotted labels as a single configuration layer. Use it when labels arrive
// from outside the configuration file — e.g. a Docker engine query, a K8s
// controller scanning service annotations, or a CLI flag with --label.
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

func newLabelProvider(labels any, opts LabelOptions) *LabelProvider {
	// Default to PriorityK8s (40). The most common label source is a K8s
	// controller forwarding metadata.labels / metadata.annotations, which
	// should sit between remote KV (30) and process env (50). Traefik /
	// Docker engine label sources whose intent is to beat env values
	// should set Priority: PriorityCLI explicitly.
	if opts.Priority == 0 {
		opts.Priority = contracts.PriorityK8s
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
