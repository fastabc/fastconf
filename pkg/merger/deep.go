// Package merger implements Kustomize-style deep merge for map[string]any
// trees.
package merger

import (
	"encoding/json"
	"fmt"
)

// Options controls deep-merge behavior.
type Options struct {
	// AppendSlices, when true, appends source slices onto destination
	// slices instead of replacing them. Rare and must be enabled
	// explicitly in _meta.
	AppendSlices bool
	// Strict, when true, errors on type mismatch. Otherwise the source
	// value silently overwrites the destination value.
	Strict bool
	// MergeKeys enables Kustomize-style strategic merge for list-of-object
	// slices. Each entry maps a dotted path in the merged tree to the
	// field name that identifies "the same item" across overlays. Example:
	//
	//	MergeKeys: map[string]string{
	//	  "spec.containers": "name",
	//	  "services":        "id",
	//	}
	//
	// When merging a slice under spec.containers, entries are aligned by
	// the value of their "name" field and merged recursively instead of
	// being appended or replaced wholesale.
	MergeKeys map[string]string
}

// Deep merges src into dst (dst is mutated in place). Runs in O(n) and
// does not allocate a new top-level map.
//
// Rules:
//   - dst[k] missing → dst[k] = src[k];
//   - both sides are maps → recurse;
//   - both sides are slices and AppendSlices → append; otherwise src
//     replaces dst;
//   - path is registered in MergeKeys → strategic merge by that key
//     field;
//   - type mismatch: Strict=true errors, otherwise src replaces dst.
func Deep(dst, src map[string]any, opt Options) error {
	return deepAt(dst, src, "", opt)
}

func deepAt(dst, src map[string]any, prefix string, opt Options) error {
	for k, sv := range src {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		dv, ok := dst[k]
		if !ok {
			dst[k] = sv
			continue
		}
		merged, err := mergeValue(path, dv, sv, opt)
		if err != nil {
			return err
		}
		dst[k] = merged
	}
	return nil
}

func mergeValue(path string, dv, sv any, opt Options) (any, error) {
	switch ds := dv.(type) {
	case map[string]any:
		ss, ok := sv.(map[string]any)
		if !ok {
			if opt.Strict {
				return nil, fmt.Errorf("merger: type mismatch at %q: have map, got %T", path, sv)
			}
			return sv, nil
		}
		if err := deepAt(ds, ss, path, opt); err != nil {
			return nil, err
		}
		return ds, nil
	case []any:
		ss, ok := sv.([]any)
		if !ok {
			if opt.Strict {
				return nil, fmt.Errorf("merger: type mismatch at %q: have slice, got %T", path, sv)
			}
			return sv, nil
		}
		// Strategic merge: if this path is registered in MergeKeys,
		// align entries by the configured key field and merge in place
		// rather than append/replace.
		if key, ok := opt.MergeKeys[path]; ok && key != "" {
			return strategicMergeList(path, ds, ss, key, opt)
		}
		if opt.AppendSlices {
			return append(ds, ss...), nil
		}
		return ss, nil
	default:
		if opt.Strict && !sameKind(dv, sv) {
			return nil, fmt.Errorf("merger: type mismatch at %q: %T vs %T", path, dv, sv)
		}
		return sv, nil
	}
}

// strategicMergeList aligns dst (a) and src (b) by the value of each
// entry's mergeKey field. Existing entries with matching keys are
// merged in place; entries with new keys are appended. Non-map entries
// are passed through unchanged.
func strategicMergeList(path string, a, b []any, key string, opt Options) ([]any, error) {
	idx := map[string]int{}
	for i, e := range a {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		k, ok := stringKey(m[key])
		if !ok {
			continue
		}
		idx[k] = i
	}
	for _, e := range b {
		m, ok := e.(map[string]any)
		if !ok {
			a = append(a, e)
			continue
		}
		k, ok := stringKey(m[key])
		if !ok {
			a = append(a, e)
			continue
		}
		if i, present := idx[k]; present {
			existing, ok := a[i].(map[string]any)
			if !ok {
				if opt.Strict {
					return nil, fmt.Errorf("merger: type mismatch at %q[%s]: have %T, got map", path, k, a[i])
				}
				a[i] = m
				continue
			}
			if err := deepAt(existing, m, path+"[]", opt); err != nil {
				return nil, err
			}
		} else {
			a = append(a, m)
			idx[k] = len(a) - 1
		}
	}
	return a, nil
}

func stringKey(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	default:
		return "", false
	}
}

func sameKind(a, b any) bool {
	switch a.(type) {
	case nil:
		return b == nil
	case bool:
		_, ok := b.(bool)
		return ok
	case string:
		_, ok := b.(string)
		return ok
	default:
		return isNumber(a) && isNumber(b)
	}
}

// isNumber matches the canonical Go numeric kinds plus json.Number.
// Stringer types deliberately do NOT count — time.Time, *os.File and
// other Stringers must not be silently treated as numbers in strict
// merges. Callers that need lenient "looks-like-a-number" semantics
// should opt into pkg/typed.Coerce instead.
func isNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		json.Number:
		return true
	}
	return false
}
