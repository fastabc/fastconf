package transform

import (
	"fmt"

	"github.com/fastabc/fastconf/pkg/mappath"
)

// LabelExpandOptions controls ExpandLabels behavior. Mirrors
// mappath.LabelOptions so callers do not need to import two packages.
type LabelExpandOptions struct {
	// Prefix, when non-empty, restricts expansion to labels whose key starts
	// with this prefix (e.g. "routing.").
	Prefix string
	// StripPrefix removes Prefix from each key before expansion.
	StripPrefix bool
	// Separator splits a flat key into nested segments. Default ".". Use
	// Separators (plural) for multi-delimiter inputs.
	Separator string
	// Separators is the ordered delimiter list (e.g. {"/", "."} for
	// K8s recommended labels). Takes precedence over Separator.
	Separators []string
	// Coerce, when true, converts "true"/"false"/int/float strings into typed
	// values. Default false: values are kept verbatim.
	Coerce bool
	// KeepSource, when true, leaves the original source list at At untouched.
	// Default false: the source list is removed after expansion so the
	// downstream struct does not need to model it.
	KeepSource bool
	// MergeMode controls how the expanded tree is grafted onto root at To:
	//   ExpandReplace (default) — set the expanded subtree at To, overwriting
	//                              any pre-existing scalar/map at that path
	//   ExpandOverlay           — deep-merge the expanded subtree on top of
	//                              any pre-existing map at To (label wins)
	//   ExpandUnderlay          — deep-merge the expanded subtree underneath
	//                              any pre-existing map at To (pre-existing wins)
	MergeMode ExpandMergeMode
}

// ExpandMergeMode selects how ExpandLabels grafts its expanded tree.
type ExpandMergeMode uint8

const (
	// ExpandReplace overwrites the value at To with the expanded subtree.
	ExpandReplace ExpandMergeMode = iota
	// ExpandOverlay deep-merges the expanded subtree on top of existing keys.
	ExpandOverlay
	// ExpandUnderlay deep-merges the expanded subtree beneath existing keys.
	ExpandUnderlay
)

// ExpandLabels returns a Transformer that reshapes a dotted label list found
// at the dotted-path `at` into a nested subtree grafted at the dotted-path
// `to`. When `to` is empty the expanded tree is grafted at the configuration
// root.
//
// Accepted input shapes at `at`:
//
//   - []string{"a.b=1", "a.c=2"}                  — Compose / docker CLI
//   - []any{"a.b=1", "a.c=2"}                     — YAML-decoded form
//   - map[string]string / map[string]any          — engine API / annotation form
//
// Example with the Compose deploy.labels shape:
//
//	# input.yaml
//	deploy:
//	  labels:
//	    - "routing.http.services.dummy.loadbalancer.server.port=9999"
//	    - "routing.enable=true"
//
//	transform.ExpandLabels("deploy.labels", "", transform.LabelExpandOptions{})
//
//	// Result (deploy.labels removed by default; KeepSource=true to keep it):
//	routing:
//	  http:
//	    services:
//	      dummy:
//	        loadbalancer:
//	          server:
//	            port: "9999"
//	    enable: "true"
//
// Malformed entries (no '=' separator, empty key after prefix trim) are
// silently dropped. A missing source path is also silently ignored (the
// transformer is a no-op).
func ExpandLabels(at, to string, opts LabelExpandOptions) Transformer {
	name := fmt.Sprintf("ExpandLabels(at=%s,to=%s)", at, to)
	return TransformerFunc{
		NameStr: name,
		Fn: func(root map[string]any) error {
			raw, ok := mappath.GetDotted(root, at)
			if !ok {
				return nil
			}
			tree := mappath.ExpandLabels(raw, mappath.LabelOptions{
				Prefix:      opts.Prefix,
				StripPrefix: opts.StripPrefix,
				Separator:   opts.Separator,
				Separators:  opts.Separators,
				Coerce:      opts.Coerce,
			})
			if len(tree) == 0 {
				if !opts.KeepSource {
					mappath.DeleteDotted(root, at)
				}
				return nil
			}
			graftLabelTree(root, to, tree, opts.MergeMode)
			if !opts.KeepSource {
				mappath.DeleteDotted(root, at)
			}
			return nil
		},
	}
}

// graftLabelTree writes tree onto root at the dotted path `to`, honouring
// MergeMode. When `to` is empty the tree is grafted at the root level.
func graftLabelTree(root map[string]any, to string, tree map[string]any, mode ExpandMergeMode) {
	if to == "" {
		for k, v := range tree {
			graftValue(root, k, v, mode)
		}
		return
	}
	existing, ok := mappath.GetDotted(root, to)
	if !ok {
		mappath.SetDotted(root, to, tree)
		return
	}
	em, isMap := existing.(map[string]any)
	if !isMap || mode == ExpandReplace {
		mappath.SetDotted(root, to, tree)
		return
	}
	for k, v := range tree {
		graftValue(em, k, v, mode)
	}
}

// graftValue installs (k, v) into dst honouring the chosen merge mode. For
// nested maps it recurses; for scalars it follows the overlay/underlay rule.
func graftValue(dst map[string]any, k string, v any, mode ExpandMergeMode) {
	prev, exists := dst[k]
	if !exists || mode == ExpandReplace {
		dst[k] = v
		return
	}
	pMap, pIsMap := prev.(map[string]any)
	vMap, vIsMap := v.(map[string]any)
	if pIsMap && vIsMap {
		for kk, vv := range vMap {
			graftValue(pMap, kk, vv, mode)
		}
		return
	}
	if mode == ExpandOverlay {
		dst[k] = v
	}
	// ExpandUnderlay: keep existing scalar — do nothing.
}
