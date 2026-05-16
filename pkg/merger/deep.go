// Package merger 实现 Kustomize 风格的 map[string]any 深度合并。
package merger

import (
	"fmt"
)

// Options 控制合并行为。
type Options struct {
	// AppendSlices 为 true 时，slice 不被替换而是追加（罕见，需在 _meta 中显式启用）。
	AppendSlices bool
	// Strict 为 true 时类型不一致直接报错；否则后者覆盖前者。
	Strict bool
	// MergeKeys (Phase 132 / SPEC-132) enables Kustomize-style strategic
	// merge for list-of-object slices. Each entry maps a dotted path in
	// the merged tree to the field name that identifies "the same item"
	// across overlays. Example:
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

// Deep 把 src 合并进 dst（dst 被原地修改）。
// 复杂度 O(n)，不分配新顶层 map。
//
// 规则：
//   - 若 dst[k] 不存在 → dst[k] = src[k]；
//   - 若两侧都为 map → 递归；
//   - 若两侧都为 slice 且 AppendSlices → 追加；否则 src 替换 dst；
//   - 若 path 在 MergeKeys 表中 → 按 mergeKey 字段做 strategic merge；
//   - 类型不一致：Strict=true 报错，否则 src 替换 dst。
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
		// Phase 132 strategic merge: if this path is registered in
		// MergeKeys, align entries by the configured key field and
		// merge in place rather than append/replace.
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
		// 数字类型放宽：int/int64/float64/json.Number 都视作可互换
		return isNumber(a) && isNumber(b)
	}
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	// json.Number 与 yaml 的字符串数字不在此处理；用 fmt.Stringer 兜底
	if s, ok := v.(fmt.Stringer); ok {
		_ = s
		return true
	}
	return false
}
