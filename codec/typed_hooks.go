// Typed hooks rewrite human-readable scalar values before the decode bridge runs. BuildTypedHookPlan
// caches target types and field aliases once, avoiding per-reload reflection while accepting both
// JSON and YAML key conventions.
package codec

import (
	"reflect"
	"slices"
	"strings"

	"github.com/fastabc/fastconf/internal/typeinfo"
)

// TypedHook converts a raw value into the typed representation that the decode bridge expects for
// a specific field type.
type TypedHook interface {
	// Match reports whether this hook applies to the given destination
	// field type. The walker calls Match once per leaf, cached by type.
	Match(t reflect.Type) bool
	// Convert turns the raw value (usually string) into a value the
	// decoder can natively assign to the target type. Returning (raw, nil)
	// leaves the value untouched. Returning a value whose type is the
	// destination type itself (e.g. *url.URL for a *url.URL field) assigns
	// it directly after decoding, for types with no string wire form.
	Convert(raw any) (any, error)
}

// TypedHookWithTarget is an optional extension to TypedHook. When a hook implements this
// interface, the walker calls ConvertWithTarget(raw, target) instead of Convert(raw), passing the
// destination field's reflect.Type. This is required for hooks that handle a family of kinds (e.g.
// StringPrimitiveHook) and need the target type to pick the right parse strategy.
type TypedHookWithTarget interface {
	TypedHook
	ConvertWithTarget(raw any, target reflect.Type) (any, error)
}

// DefaultTypedHooks returns the built-in hook set. Hook implementations and ordering are defined
// in typed_hooks_defaults.go. DurationHook is registered first so named primitive types win
// dispatch over the generic StringPrimitiveHook. Install additional hooks via WithTypedHook.
func DefaultTypedHooks() []TypedHook {
	return defaultTypedHooks()
}

// PlanOption customizes BuildTypedHookPlan.
type PlanOption func(*planBuilder)

// WithYAMLFields makes the plan select fields the way the yaml.v3 decode bridge does: embedded
// structs flatten only with `yaml:",inline"`. The default follows encoding/json, which flattens
// untagged anonymous structs.
func WithYAMLFields() PlanOption {
	return func(b *planBuilder) { b.yaml = true }
}

// TypedHookPlan mirrors the destination type: struct fields (with every map-key alias the merged
// map might use), slice/map elements and hook leaves. Recursive types share one node per type, so
// the plan is finite while Apply follows the finite input tree.
type TypedHookPlan struct {
	root *typePlan
}

// typePlan describes how to rewrite one value of a destination type.
type typePlan struct {
	// hook applies to this node when non-nil; it excludes fields and elem.
	hook TypedHook
	// target is the destination type associated with hook; passed to
	// hooks that implement TypedHookWithTarget.
	target reflect.Type
	// direct marks targets with no scalar wire form: a converted value of
	// exactly this type is assigned after decoding.
	direct bool
	fields []*fieldPlan // struct fields
	elem   *typePlan    // slice, array or map elements
	// live: a hook is reachable. assigns: a direct hook is reachable.
	live, assigns bool
}

type fieldPlan struct {
	aliases []string // candidate map keys (json, yaml, lowercase, exact)
	index   []int    // path from the owning struct, through flattened embeds
	plan    *typePlan
}

type planBuilder struct {
	hooks []TypedHook
	yaml  bool
	memo  map[reflect.Type]*typePlan
	all   []*typePlan
}

// BuildTypedHookPlan inspects t and returns a plan that the walker can apply against any merged
// map. Pointer/elem unwrapping is automatic.
func BuildTypedHookPlan(t reflect.Type, hooks []TypedHook, opts ...PlanOption) *TypedHookPlan {
	if len(hooks) == 0 || t == nil {
		return &TypedHookPlan{}
	}
	b := &planBuilder{hooks: hooks, memo: map[reflect.Type]*typePlan{}}
	for _, opt := range opts {
		opt(b)
	}
	root := b.planFor(t)
	b.propagate()
	if !root.live {
		return &TypedHookPlan{}
	}
	return &TypedHookPlan{root: root}
}

// planFor returns the plan for a destination type. Hook matches are checked against both the
// declared type (e.g. *url.URL) and its dereferenced base; otherwise the base shape is shared.
func (b *planBuilder) planFor(t reflect.Type) *typePlan {
	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	for _, h := range b.hooks {
		target := t
		if !h.Match(t) {
			if base == t || !h.Match(base) {
				continue
			}
			target = base
		}
		tp := &typePlan{hook: h, target: target, direct: directTarget(target), live: true}
		tp.assigns = tp.direct
		return tp
	}
	return b.shapeFor(base)
}

// shapeFor memoizes container shapes before descending, so a self-referential type resolves to
// its own node instead of recursing forever.
func (b *planBuilder) shapeFor(t reflect.Type) *typePlan {
	if tp, ok := b.memo[t]; ok {
		return tp
	}
	tp := &typePlan{}
	b.memo[t] = tp
	b.all = append(b.all, tp)
	switch t.Kind() {
	case reflect.Struct:
		if b.yaml {
			b.yamlFields(tp, t, nil, map[reflect.Type]bool{})
		} else {
			for _, f := range typeinfo.JSONFields(t) {
				b.addField(tp, f.Field, f.Field.Index)
			}
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		tp.elem = b.planFor(t.Elem())
	}
	return tp
}

// yamlFields follows yaml.v3: exported fields keyed by tag or lowercase name; `,inline` structs
// contribute their fields to the parent.
func (b *planBuilder) yamlFields(tp *typePlan, t reflect.Type, prefix []int, stack map[reflect.Type]bool) {
	if stack[t] {
		return
	}
	stack[t] = true
	defer delete(stack, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		index := append(append([]int(nil), prefix...), i)
		if inlineYAML(f.Tag.Get("yaml")) {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				b.yamlFields(tp, ft, index, stack)
			}
			continue
		}
		b.addField(tp, f, index)
	}
}

func (b *planBuilder) addField(tp *typePlan, f reflect.StructField, index []int) {
	aliases := typeinfo.ResolveField(f).Candidates
	if len(aliases) == 0 {
		return
	}
	tp.fields = append(tp.fields, &fieldPlan{aliases: aliases, index: index, plan: b.planFor(f.Type)})
}

// propagate computes live/assigns to a fixed point (shapes may form cycles), then drops subtrees
// with no reachable hook so Apply never visits them.
func (b *planBuilder) propagate() {
	for changed := true; changed; {
		changed = false
		for _, tp := range b.all {
			live, assigns := tp.live, tp.assigns
			for _, f := range tp.fields {
				live = live || f.plan.live
				assigns = assigns || f.plan.assigns
			}
			if tp.elem != nil {
				live = live || tp.elem.live
				assigns = assigns || tp.elem.assigns
			}
			if live != tp.live || assigns != tp.assigns {
				tp.live, tp.assigns, changed = live, assigns, true
			}
		}
	}
	for _, tp := range b.all {
		fields := tp.fields[:0]
		for _, f := range tp.fields {
			if f.plan.live {
				fields = append(fields, f)
			}
		}
		tp.fields = fields
		if tp.elem != nil && !tp.elem.live {
			tp.elem = nil
		}
	}
}

// directTarget reports whether a destination type has no scalar wire form the bridges accept, so
// a typed hook result must be assigned after decoding.
func directTarget(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Struct, reflect.Pointer, reflect.Interface:
		return true
	}
	return false
}

func inlineYAML(tag string) bool {
	_, flags, _ := strings.Cut(tag, ",")
	return slices.Contains(strings.Split(flags, ","), "inline")
}

// Apply rewrites every hook-eligible leaf in merged. Returns the first conversion error
// encountered.
func (p *TypedHookPlan) Apply(merged map[string]any) error {
	if p == nil || p.root == nil {
		return nil
	}
	_, err := p.root.apply(merged)
	return err
}

// apply returns v rewritten in place where possible; the caller stores the result.
func (tp *typePlan) apply(v any) (any, error) {
	if tp.hook != nil {
		var converted any
		var err error
		if hwt, ok := tp.hook.(TypedHookWithTarget); ok && tp.target != nil {
			converted, err = hwt.ConvertWithTarget(v, tp.target)
		} else {
			converted, err = tp.hook.Convert(v)
		}
		if tp.direct && err == nil && converted != nil && reflect.TypeOf(converted) == tp.target {
			converted = assignedValue{converted}
		}
		return converted, err
	}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	switch node := v.(type) {
	case map[string]any:
		if tp.elem != nil {
			for k, child := range node {
				converted, err := tp.elem.apply(child)
				keep(err)
				node[k] = converted
			}
			return node, firstErr
		}
		for _, f := range tp.fields {
			key, present := pickAlias(node, f.aliases)
			if !present {
				continue
			}
			converted, err := f.plan.apply(node[key])
			keep(err)
			node[key] = converted
		}
	case []any:
		if tp.elem != nil {
			for i, child := range node {
				converted, err := tp.elem.apply(child)
				keep(err)
				node[i] = converted
			}
		}
	}
	return v, firstErr
}

// assignedValue carries a hook result of its destination type through the decode bridge. It
// encodes as null, leaving the field for Assign to set after decoding.
type assignedValue struct{ v any }

func (assignedValue) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
func (assignedValue) MarshalYAML() (any, error)    { return nil, nil }

// Assign stores hook results that are values of their destination type (see TypedHook.Convert)
// into target, a pointer to the decoded value. It is a no-op unless such a hook applies.
func (p *TypedHookPlan) Assign(merged map[string]any, target any) {
	if p == nil || p.root == nil || !p.root.assigns {
		return
	}
	p.root.assign(merged, reflect.ValueOf(target))
}

func (tp *typePlan) assign(raw any, dst reflect.Value) {
	if !tp.assigns || raw == nil {
		return
	}
	if tp.hook != nil {
		a, ok := raw.(assignedValue)
		if !ok {
			return
		}
		if dst = settle(dst, tp.target); dst.IsValid() {
			dst.Set(reflect.ValueOf(a.v))
		}
		return
	}
	dst = settle(dst, nil)
	if !dst.IsValid() {
		return
	}
	switch node := raw.(type) {
	case map[string]any:
		switch {
		case tp.elem != nil && dst.Kind() == reflect.Map:
			if dst.IsNil() || dst.Type().Key().Kind() != reflect.String {
				return
			}
			for k, child := range node {
				key := reflect.ValueOf(k).Convert(dst.Type().Key())
				cur := dst.MapIndex(key)
				if !cur.IsValid() {
					continue
				}
				elem := reflect.New(cur.Type()).Elem()
				elem.Set(cur)
				tp.elem.assign(child, elem)
				dst.SetMapIndex(key, elem)
			}
		case dst.Kind() == reflect.Struct:
			for _, f := range tp.fields {
				if key, ok := pickAlias(node, f.aliases); ok {
					f.plan.assign(node[key], fieldAt(dst, f.index))
				}
			}
		}
	case []any:
		if tp.elem != nil && (dst.Kind() == reflect.Slice || dst.Kind() == reflect.Array) {
			for i := 0; i < len(node) && i < dst.Len(); i++ {
				tp.elem.assign(node[i], dst.Index(i))
			}
		}
	}
}

// settle dereferences dst, allocating nil pointers, until it has type want (or, with want nil,
// until it is no longer a pointer). It returns the zero Value when dst cannot be set.
func settle(dst reflect.Value, want reflect.Type) reflect.Value {
	for dst.IsValid() && dst.Type() != want && dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			if !dst.CanSet() {
				return reflect.Value{}
			}
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		dst = dst.Elem()
	}
	if !dst.IsValid() || !dst.CanSet() || (want != nil && dst.Type() != want) {
		return reflect.Value{}
	}
	return dst
}

// fieldAt follows a field index through embedded structs, allocating nil embedded pointers.
func fieldAt(v reflect.Value, index []int) reflect.Value {
	for n, i := range index {
		if n > 0 {
			if v = settle(v, nil); !v.IsValid() || v.Kind() != reflect.Struct {
				return reflect.Value{}
			}
		}
		v = v.Field(i)
	}
	return v
}

// pickAlias returns the first alias that exists in node, plus whether any matched.
func pickAlias(node map[string]any, aliases []string) (string, bool) {
	for _, a := range aliases {
		if _, ok := node[a]; ok {
			return a, true
		}
	}
	return "", false
}
