package provider

import (
	"strings"

	"github.com/fastabc/fastconf/pkg/mappath"
)

// collectRoutingLabelPairs normalises any of the accepted label inputs
// ([]string, map[string]string, ...) into a pair slice with each key
// optionally lowercased.
func collectRoutingLabelPairs(input any, lowercase bool) []mappath.LabelPair {
	pairs := mappath.NormalizeLabelInput(input)
	out := make([]mappath.LabelPair, 0, len(pairs))
	for _, p := range pairs {
		p.Key = normalizeRoutingKey(p.Key, lowercase)
		out = append(out, p)
	}
	return out
}

// routingPairsAsList re-renders pairs into the "key=value" form expected
// by mappath.ExpandLabels.
func routingPairsAsList(pairs []mappath.LabelPair) []string {
	out := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, pair.Key+"="+pair.Value)
	}
	return out
}

// normalizeRoutingKey applies the lowercase-keys option uniformly across
// the inspection and expansion paths.
func normalizeRoutingKey(key string, lowercase bool) string {
	if lowercase {
		return strings.ToLower(key)
	}
	return key
}

// routingGateBlocks returns true when an EnableGate has been configured
// and its value is missing or not truthy. Callers short-circuit Load on
// true so a half-configured deployment cannot leak routing labels.
func routingGateBlocks(pairs []mappath.LabelPair, gate string) bool {
	if gate == "" {
		return false
	}
	var (
		value string
		found bool
	)
	for _, pair := range pairs {
		if pair.Key == gate {
			value = pair.Value
			found = true
		}
	}
	return found && !routingTruthy(value)
}

// routingTruthy mirrors Traefik / Docker label truthy semantics — case
// insensitive, surrounding whitespace tolerated.
func routingTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
