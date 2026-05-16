// Phase 130 (SPEC-130) — typed decoder hooks.
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
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
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

// DefaultTypedHooks returns the built-in hook set. By design only
// hooks whose target type has a native JSON wire form survive the
// pre-decode rewrite (encoding/json must accept the rewritten value).
// time.Duration → int64 nanoseconds is the canonical example; *url.URL
// and *regexp.Regexp have no direct JSON form and are exposed as
// helpers users can install explicitly via WithTypedHook when their
// schema uses a string-field surrogate.
func DefaultTypedHooks() []TypedHook {
	return []TypedHook{DurationHook{}}
}

// DurationHook: "30s" → int64(30 * time.Second).
type DurationHook struct{}

var durationType = reflect.TypeOf(time.Duration(0))

func (DurationHook) Match(t reflect.Type) bool { return t == durationType }

func (DurationHook) Convert(raw any) (any, error) {
	switch v := raw.(type) {
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			return raw, fmt.Errorf("duration hook: %w", err)
		}
		return int64(d), nil
	default:
		return raw, nil
	}
}

// IPHook: "10.0.0.1" → canonical string accepted by net.IP JSON unmarshal.
type IPHook struct{}

var ipType = reflect.TypeOf(net.IP{})

func (IPHook) Match(t reflect.Type) bool { return t == ipType }

func (IPHook) Convert(raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return raw, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return raw, fmt.Errorf("ip hook: cannot parse %q", s)
	}
	return ip.String(), nil
}

// URLHook: round-trips a URL string after validating.
type URLHook struct{}

var (
	urlType    = reflect.TypeOf((*url.URL)(nil))
	urlValType = reflect.TypeOf(url.URL{})
)

func (URLHook) Match(t reflect.Type) bool { return t == urlType || t == urlValType }

func (URLHook) Convert(raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return raw, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return raw, fmt.Errorf("url hook: %w", err)
	}
	return u.String(), nil
}

// RegexHook: validates the pattern then passes through.
type RegexHook struct{}

var regexType = reflect.TypeOf((*regexp.Regexp)(nil))

func (RegexHook) Match(t reflect.Type) bool { return t == regexType }

func (RegexHook) Convert(raw any) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return raw, nil
	}
	if _, err := regexp.Compile(s); err != nil {
		return raw, fmt.Errorf("regex hook: %w", err)
	}
	return s, nil
}

// TypedHookPlan is a struct-shaped plan: each node mirrors a struct
// field and records which map-key aliases the merged map might use
// (json tag, yaml tag, lowercase name, exact field name).
type TypedHookPlan struct {
	// hook applies to this node when non-nil. Mutually exclusive with children.
	hook TypedHook
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
		for _, h := range p.hooks {
			if h.Match(ft) || h.Match(base) {
				matched = h
				break
			}
		}
		if matched != nil {
			child.hook = matched
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
			converted, err := child.plan.hook.Convert(v)
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
