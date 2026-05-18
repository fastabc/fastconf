package provider

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/pkg/mappath"
)

var indexedRoutingKeyPattern = regexp.MustCompile(`^(.+)\[(\d+)\]$`)

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
	// Name overrides the default provider name ("labels:routing" or
	// "labels:routing:<prefix>" when Prefix is non-empty).
	Name string
	// Priority sets the merge priority. Defaults to PriorityStatic (10).
	Priority int

	// Prefix, when non-empty, restricts expansion to labels whose key starts
	// with the prefix.
	Prefix string
	// StripPrefix removes Prefix from each key before expansion.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".".
	Separator string
	// Separators is the ordered list of delimiters applied to each key. When
	// set, it takes precedence over Separator.
	Separators []string

	// EnableGate names an optional label consulted before expansion. When the
	// key is present and its value is not truthy ("true", "1", "yes", "on"),
	// the whole label set is skipped.
	EnableGate string

	// ListSeparator splits list-valued leaves. Empty falls back to ",".
	ListSeparator string
	// NoListSplit keeps list-looking values as one scalar leaf.
	NoListSplit bool
	// KeepRawSuffixes lists lower-case key suffixes whose values must remain a
	// raw string even when they contain ListSeparator. nil uses the routing
	// defaults {".rule", "regexp"}; an explicit empty slice disables this
	// protection list.
	KeepRawSuffixes []string

	// Raw keeps every leaf as a string, disabling scalar coercion and list
	// splitting while still performing dotted expansion and indexed promotion.
	Raw bool
	// LowercaseKeys lowercases the full input key before filtering and
	// expansion. Disabled by default so provider-neutral routing DSLs preserve
	// their original key identity.
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
	rewriteRoutingLeaves(tree, nil, p.opts)
	PromoteIndexedRoutingKeys(tree)
	return tree, nil
}

// Watch implements Provider. Routing labels are static after registration;
// callers with a live upstream should trigger Manager.Reload(ctx) themselves.
func (p *RoutingLabelProvider) Watch(_ context.Context) (<-chan contracts.Event, error) {
	return nil, nil
}

type routingLabelPair struct {
	key   string
	value string
}

func collectRoutingLabelPairs(input any, lowercase bool) []routingLabelPair {
	var out []routingLabelPair
	appendPair := func(k, v string) {
		out = append(out, routingLabelPair{
			key:   normalizeRoutingKey(k, lowercase),
			value: v,
		})
	}

	switch labels := input.(type) {
	case []string:
		for _, kv := range labels {
			if k, v, ok := strings.Cut(kv, "="); ok {
				appendPair(k, v)
			}
		}
	case map[string]string:
		for k, v := range labels {
			appendPair(k, v)
		}
	}
	return out
}

func routingPairsAsList(pairs []routingLabelPair) []string {
	out := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, pair.key+"="+pair.value)
	}
	return out
}

func normalizeRoutingKey(key string, lowercase bool) string {
	if lowercase {
		return strings.ToLower(key)
	}
	return key
}

func routingGateBlocks(pairs []routingLabelPair, gate string) bool {
	if gate == "" {
		return false
	}
	var (
		value string
		found bool
	)
	for _, pair := range pairs {
		if pair.key == gate {
			value = pair.value
			found = true
		}
	}
	return found && !routingTruthy(value)
}

func routingTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func rewriteRoutingLeaves(node map[string]any, path []string, opts RoutingLabelOptions) {
	for key, value := range node {
		nextPath := append(append([]string(nil), path...), key)
		switch typed := value.(type) {
		case map[string]any:
			rewriteRoutingLeaves(typed, nextPath, opts)
		case string:
			node[key] = rewriteRoutingLeaf(typed, strings.Join(nextPath, "."), opts)
		}
	}
}

func rewriteRoutingLeaf(value, dottedPath string, opts RoutingLabelOptions) any {
	if opts.Raw {
		return value
	}
	if routingKeepRaw(dottedPath, opts.KeepRawSuffixes) {
		return value
	}
	separator := opts.ListSeparator
	if separator == "" {
		separator = ","
	}
	if !opts.NoListSplit && separator != "" && strings.Contains(value, separator) {
		rawParts := strings.Split(value, separator)
		parts := make([]any, 0, len(rawParts))
		for _, part := range rawParts {
			parts = append(parts, coerceRoutingScalar(strings.TrimSpace(part)))
		}
		return parts
	}
	return coerceRoutingScalar(value)
}

func routingKeepRaw(dottedPath string, configured []string) bool {
	suffixes := configured
	if suffixes == nil {
		suffixes = []string{".rule", "regexp"}
	}
	lowerPath := strings.ToLower(dottedPath)
	for _, suffix := range suffixes {
		if suffix == "" {
			continue
		}
		if strings.HasSuffix(lowerPath, strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

func coerceRoutingScalar(value string) any {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
		return f
	}
	return value
}

// PromoteIndexedRoutingKeys collapses sibling groups such as
// "domains[0]" / "domains[1]" into "domains": []any{...}. Groups that would
// collide with an existing base key are left untouched.
func PromoteIndexedRoutingKeys(node map[string]any) {
	for _, value := range node {
		if child, ok := value.(map[string]any); ok {
			PromoteIndexedRoutingKeys(child)
		}
	}

	type indexedGroup struct {
		max    int
		values map[int]any
		keys   []string
	}
	groups := map[string]*indexedGroup{}
	for key, value := range node {
		matches := indexedRoutingKeyPattern.FindStringSubmatch(key)
		if len(matches) != 3 {
			continue
		}
		index, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}
		base := matches[1]
		group := groups[base]
		if group == nil {
			group = &indexedGroup{max: -1, values: map[int]any{}}
			groups[base] = group
		}
		group.keys = append(group.keys, key)
		group.values[index] = value
		if index > group.max {
			group.max = index
		}
	}

	for base, group := range groups {
		if _, exists := node[base]; exists {
			continue
		}
		items := make([]any, group.max+1)
		for index, value := range group.values {
			items[index] = value
		}
		for _, key := range group.keys {
			delete(node, key)
		}
		node[base] = items
	}
}
