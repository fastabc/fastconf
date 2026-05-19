// Typed decoder hooks.
//
// FastConf's default decode path round-trips merged maps through
// encoding/json. That keeps the canonical byte stream coherent with the
// SHA-256 hash used for dedupe, but encoding/json refuses common
// "human-readable string → typed value" conversions: "30s" cannot land
// in a time.Duration, "10.0.0.1" cannot land in net.IP, etc.
//
// TypedHook is a small, opt-in pre-decode rewrite step. BuildTypedPlan
// inspects *T once at construction and emits a tree describing which
// leaves carry hook-eligible types and which map-key candidates they
// can appear under (json tag, lowercase field name, struct field name).
// Apply walks both the merged map and the plan tree together so it
// works regardless of whether the source codec wrote canonical-JSON
// keys or YAML-flavoured lowercase keys.
package decoder

import (
	"reflect"
	"strings"
)

// TypedHook converts a raw value into the typed representation that
// encoding/json round-trip expects for a specific field type.
type TypedHook interface {
	// Match reports whether this hook applies to the given destination
	// field type. The walker calls Match once per leaf, cached by type.
	Match(t reflect.Type) bool
	// Convert turns the raw value (usually string) into a value the
	// JSON decoder can natively assign to the target type. Returning
	// (raw, nil) leaves the value untouched.
	Convert(raw any) (any, error)
}

// TypedHookWithTarget is an optional extension to TypedHook. When a hook
// implements this interface, the walker calls ConvertWithTarget(raw,
// target) instead of Convert(raw), passing the destination field's
// reflect.Type. This is required for hooks that handle a family of
// kinds (e.g. StringPrimitiveHook) and need the target type to pick the
// right parse strategy.
type TypedHookWithTarget interface {
	TypedHook
	ConvertWithTarget(raw any, target reflect.Type) (any, error)
}

// DefaultTypedHooks returns the built-in hook set. Hook implementations
// and ordering are defined in typed_hooks_defaults.go. DurationHook is
// registered first so named primitive types win dispatch over the
// generic StringPrimitiveHook. Install additional hooks via WithTypedHook.
func DefaultTypedHooks() []TypedHook {
	return defaultTypedHooks()
}

// TypedHookPlan is a struct-shaped plan: each node mirrors a struct
// field and records which map-key aliases the merged map might use
// (json tag, yaml tag, lowercase name, exact field name).
type TypedHookPlan struct {
	// hook applies to this node when non-nil. Mutually exclusive with children.
	hook TypedHook
	// hookTarget is the destination field type associated with hook; passed
	// to hooks that implement TypedHookWithTarget so they can switch on the
	// destination kind at Convert time.
	hookTarget reflect.Type
	// children, when non-empty, recurses into nested struct fields.
	// Keyed by canonical alias (lowercase field name); each child also
	// exposes its alias list so the walker can locate the corresponding
	// key in the merged map.
	children []*planNode
	hooks    []TypedHook
}

type planNode struct {
	aliases []string // candidate map keys (lowercase, json tag, yaml tag)
	plan    *TypedHookPlan
}

// BuildTypedHookPlan inspects t and returns a plan that the walker can
// apply against any merged map. Pointer/elem unwrapping is automatic.
func BuildTypedHookPlan(t reflect.Type, hooks []TypedHook) *TypedHookPlan {
	if len(hooks) == 0 {
		return &TypedHookPlan{hooks: nil}
	}
	root := &TypedHookPlan{hooks: hooks}
	root.build(t)
	return root
}

func (p *TypedHookPlan) build(t reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		aliases := fieldAliases(f)
		if len(aliases) == 0 {
			continue
		}
		// Check hooks against both the original type (e.g. *url.URL)
		// and the dereferenced struct type.
		ft := f.Type
		base := ft
		for base.Kind() == reflect.Ptr {
			base = base.Elem()
		}
		child := &TypedHookPlan{hooks: p.hooks}
		var matched TypedHook
		var matchedTarget reflect.Type
		for _, h := range p.hooks {
			switch {
			case h.Match(ft):
				matched = h
				matchedTarget = ft
			case h.Match(base):
				matched = h
				matchedTarget = base
			}
			if matched != nil {
				break
			}
		}
		if matched != nil {
			child.hook = matched
			child.hookTarget = matchedTarget
		} else if base.Kind() == reflect.Struct {
			child.build(base)
			if len(child.children) == 0 {
				continue
			}
		} else {
			continue
		}
		p.children = append(p.children, &planNode{aliases: aliases, plan: child})
	}
}

// Apply rewrites every hook-eligible leaf in merged. Returns the first
// conversion error encountered.
func (p *TypedHookPlan) Apply(merged map[string]any) error {
	if p == nil || len(p.children) == 0 {
		return nil
	}
	return p.walk(merged)
}

func (p *TypedHookPlan) walk(node map[string]any) error {
	var firstErr error
	for _, child := range p.children {
		key, present := pickAlias(node, child.aliases)
		if !present {
			continue
		}
		v := node[key]
		if child.plan.hook != nil {
			var converted any
			var err error
			if hwt, ok := child.plan.hook.(TypedHookWithTarget); ok && child.plan.hookTarget != nil {
				converted, err = hwt.ConvertWithTarget(v, child.plan.hookTarget)
			} else {
				converted, err = child.plan.hook.Convert(v)
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
			node[key] = converted
			continue
		}
		// Recurse into nested struct.
		nested, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if err := child.plan.walk(nested); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// fieldAliases returns the candidate map keys for a struct field,
// preferring json tag → yaml tag → lowercase name → field name.
func fieldAliases(f reflect.StructField) []string {
	out := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == "-" {
			return
		}
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	add(stripTag(f.Tag.Get("json")))
	add(stripTag(f.Tag.Get("yaml")))
	add(strings.ToLower(f.Name))
	add(f.Name)
	return out
}

func stripTag(t string) string {
	if i := strings.IndexByte(t, ','); i >= 0 {
		return t[:i]
	}
	return t
}

// pickAlias returns the first alias that exists in node, plus whether
// any matched.
func pickAlias(node map[string]any, aliases []string) (string, bool) {
	for _, a := range aliases {
		if _, ok := node[a]; ok {
			return a, true
		}
	}
	return "", false
}
