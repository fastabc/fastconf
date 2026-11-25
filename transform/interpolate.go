package transform

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/fastabc/fastconf/confmap"
)

// InterpolateOptions controls environment lookup and dotted configuration references.
type InterpolateOptions struct {
	Lookup func(name string) (string, bool)
	Refs   bool
}

var interpolatePattern = regexp.MustCompile(`\$\$\{|\$\{([A-Za-z_][A-Za-z0-9_.]*)(?::([-?]?)([^}]*))?\}`)

// Interpolate expands environment variables and, optionally, dotted scalar
// references. Lookup results are literal and never expanded a second time.
func Interpolate(opts InterpolateOptions) Func {
	if opts.Lookup == nil {
		opts.Lookup = os.LookupEnv
	}
	return func(root map[string]any) error {
		r := interpolation{opts: opts, root: root, memo: make(map[string]string)}
		// Resolve against the original tree until every value succeeds. Otherwise
		// a reference could expand an already substituted environment value.
		expanded, err := r.walk(root, "", 0)
		if err != nil {
			return err
		}
		maps.Copy(root, expanded.(map[string]any))
		return nil
	}
}

type interpolation struct {
	opts  InterpolateOptions
	root  map[string]any
	memo  map[string]string
	stack []string
}

func (r *interpolation) reference(path string) (string, bool, error) {
	if value, ok := r.memo[path]; ok {
		return value, true, nil
	}
	if slices.Contains(r.stack, path) {
		return "", false, fmt.Errorf("cycle: %s", strings.Join(append(r.stack, path), " -> "))
	}
	if len(r.stack) >= 32 {
		return "", false, fmt.Errorf("maximum reference depth 32 exceeded")
	}
	value, ok := confmap.GetDotted(r.root, path)
	if !ok {
		return "", false, nil
	}
	switch v := value.(type) {
	case string:
		r.stack = append(r.stack, path)
		out, err := r.expand(v, path)
		r.stack = r.stack[:len(r.stack)-1]
		if err != nil {
			return "", false, err
		}
		r.memo[path] = out
		return out, true, nil
	case json.Number:
		return v.String(), true, nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(v), true, nil
	default:
		return "", false, fmt.Errorf("reference %s is not a scalar", path)
	}
}

func (r *interpolation) expand(s, path string) (string, error) {
	var first error
	out := interpolatePattern.ReplaceAllStringFunc(s, func(match string) string {
		value, err := r.replace(match)
		if first == nil {
			first = err
		}
		return value
	})
	if first != nil {
		return "", fmt.Errorf("interpolate %s: %w", path, first)
	}
	return out, nil
}

func (r *interpolation) replace(match string) (string, error) {
	if match == "$${" {
		return "${", nil
	}
	m := interpolatePattern.FindStringSubmatch(match)
	name, op, body := m[1], m[2], m[3]
	isRef := strings.Contains(name, ".")
	var value string
	var ok bool
	if isRef {
		if !r.opts.Refs {
			return match, nil
		}
		var err error
		value, ok, err = r.reference(name)
		if err != nil {
			return "", err
		}
	} else {
		value, ok = r.opts.Lookup(name)
	}
	if ok && value != "" {
		return value, nil
	}
	if op == "?" {
		if body == "" {
			body = "value is required"
		}
		return "", fmt.Errorf("%w: %s", errRequired, body)
	}
	if strings.Contains(match, ":") {
		return body, nil
	}
	if isRef && !ok {
		return "", fmt.Errorf("reference %s not found", name)
	}
	return value, nil
}

func (r *interpolation) walk(node any, path string, depth int) (any, error) {
	if depth > 256 {
		return nil, fmt.Errorf("interpolate %s: maximum tree depth exceeded", path)
	}
	switch n := node.(type) {
	case string:
		return r.expand(n, path)
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			p := k
			if path != "" {
				p = path + "." + k
			}
			var err error
			out[k], err = r.walk(v, p, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			var err error
			out[i], err = r.walk(v, fmt.Sprintf("%s.%d", path, i), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return node, nil
}
