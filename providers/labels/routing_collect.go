package labels

import (
	"strings"

	"github.com/fastabc/fastconf/confmap"
)

// collectRoutingLabelPairs normalises any of the accepted label inputs ([]string,
// map[string]string, ...) into a pair slice with each key optionally lowercased.
func collectRoutingLabelPairs(input any, lowercase bool) []confmap.LabelPair {
	pairs := confmap.NormalizeLabelInput(input)
	for i := range pairs {
		pairs[i].Key = normalizeRoutingKey(pairs[i].Key, lowercase)
	}
	return pairs
}

// normalizeRoutingKey applies the lowercase-keys option uniformly across the inspection and
// expansion paths.
func normalizeRoutingKey(key string, lowercase bool) string {
	if lowercase {
		return strings.ToLower(key)
	}
	return key
}

// routingGateBlocks skips labels only when the gate is present and not truthy.
// The last matching pair wins; an absent gate leaves the set enabled.
func routingGateBlocks(pairs []confmap.LabelPair, gate string) bool {
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

// routingTruthy mirrors Traefik / Docker label truthy semantics — case insensitive, surrounding
// whitespace tolerated.
func routingTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
