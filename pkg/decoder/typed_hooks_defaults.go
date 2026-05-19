// Built-in TypedHook implementations.
package decoder

import (
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// defaultTypedHooks returns the hook slice used by DefaultTypedHooks.
// Keeping the constructor private lets the public API delegate cleanly
// while keeping the implementation next to the hook types.
func defaultTypedHooks() []TypedHook {
	return []TypedHook{DurationHook{}, StringPrimitiveHook{}}
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

// StringPrimitiveHook converts string raw values into the primitive
// kind expected by the destination struct field: bool, int family, uint
// family, or float family. Named primitive types (e.g. time.Duration)
// are deliberately skipped so dedicated hooks (DurationHook) win the
// dispatch instead.
//
// The hook is a no-op when the raw value is not a string, so YAML/JSON
// layers that already decoded values to typed forms pass through
// unchanged. It is registered in DefaultTypedHooks so the typical
// "env value lands in a typed struct field" path works out of the box.
type StringPrimitiveHook struct{}

// Match accepts unnamed (builtin) primitive numeric / bool types.
// Named types (PkgPath != "") are excluded so DurationHook and similar
// keep their precedence.
func (StringPrimitiveHook) Match(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if t.PkgPath() != "" {
		return false
	}
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// Convert without a target type cannot pick a parse strategy, so it
// returns raw untouched. The walker uses ConvertWithTarget when the
// plan node records a target type.
func (StringPrimitiveHook) Convert(raw any) (any, error) { return raw, nil }

// ConvertWithTarget parses the string into the requested kind. Non-string
// raw values pass through unchanged.
func (StringPrimitiveHook) ConvertWithTarget(raw any, target reflect.Type) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return raw, nil
	}
	switch target.Kind() {
	case reflect.Bool:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off", "":
			return false, nil
		}
		return raw, fmt.Errorf("string-primitive hook: cannot parse %q as bool", s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return raw, fmt.Errorf("string-primitive hook: parse int: %w", err)
		}
		return n, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return raw, fmt.Errorf("string-primitive hook: parse uint: %w", err)
		}
		return n, nil
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return raw, fmt.Errorf("string-primitive hook: parse float: %w", err)
		}
		return f, nil
	}
	return raw, nil
}
