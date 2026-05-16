package obs

import "github.com/fastabc/fastconf/contracts"

// EnrichAttrs fans attribute pairs into sp via SetAttribute.
func EnrichAttrs(sp contracts.Span, attrs ...contracts.Attr) {
	if sp == nil {
		return
	}
	for _, a := range attrs {
		sp.SetAttribute(a.Key, a.Value)
	}
}

// EnrichSpan keeps the map-based adapter for existing call sites.
func EnrichSpan(sp contracts.Span, attrs map[string]any) {
	if sp == nil {
		return
	}
	for k, v := range attrs {
		sp.SetAttribute(k, v)
	}
}
