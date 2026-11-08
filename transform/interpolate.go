package transform

import (
	"fmt"
	"os"
	"regexp"
	"strings"
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
	lookup := opts.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return func(root map[string]any) error {
		memo := map[string]string{}
		active := map[string]bool{}
		var stack []string
		var expand func(string, string) (string, error)
		var ref func(string) (string, bool, error)
		ref = func(path string) (string, bool, error) {
			if v, ok := memo[path]; ok {
				return v, true, nil
			}
			if active[path] {
				return "", false, fmt.Errorf("cycle: %s", strings.Join(append(stack, path), " -> "))
			}
			if len(stack) >= 32 {
				return "", false, fmt.Errorf("maximum reference depth 32 exceeded")
			}
			var value any = root
			for part := range strings.SplitSeq(path, ".") {
				obj, ok := value.(map[string]any)
				if !ok {
					return "", false, nil
				}
				value, ok = obj[part]
				if !ok {
					return "", false, nil
				}
			}
			if s, ok := value.(string); ok {
				active[path] = true
				stack = append(stack, path)
				out, err := expand(s, path)
				stack = stack[:len(stack)-1]
				delete(active, path)
				if err != nil {
					return "", false, err
				}
				memo[path] = out
				return out, true, nil
			}
			switch value.(type) {
			case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
				return fmt.Sprint(value), true, nil
			}
			// json.Number is a named string, distinct from ordinary string values.
			if n, ok := value.(interface{ String() string }); ok {
				return n.String(), true, nil
			}
			return "", false, fmt.Errorf("reference %s is not a scalar", path)
		}
		expand = func(s, path string) (string, error) {
			var first error
			out := interpolatePattern.ReplaceAllStringFunc(s, func(match string) string {
				if match == "$${" {
					return "${"
				}
				m := interpolatePattern.FindStringSubmatch(match)
				name, op, body := m[1], m[2], m[3]
				var v string
				var ok bool
				isRef := strings.Contains(name, ".")
				if isRef {
					if !opts.Refs {
						return match
					}
					var err error
					v, ok, err = ref(name)
					if err != nil {
						if first == nil {
							first = err
						}
						return ""
					}
				} else {
					v, ok = lookup(name)
				}
				if ok && v != "" {
					return v
				}
				if op == "?" {
					if body == "" {
						body = "value is required"
					}
					if first == nil {
						first = fmt.Errorf("%w: %s", errRequired, body)
					}
					return ""
				}
				if strings.Contains(match, ":") {
					return body
				}
				if isRef && !ok && first == nil {
					first = fmt.Errorf("reference %s not found", name)
				}
				return v
			})
			if first != nil {
				return "", fmt.Errorf("interpolate %s: %w", path, first)
			}
			return out, nil
		}
		// Read the original tree until every value has been expanded, so traversal
		// order cannot expand a previously inserted environment value or escape.
		var walk func(any, string, int) (any, error)
		walk = func(node any, path string, depth int) (any, error) {
			if depth > 256 {
				return nil, fmt.Errorf("interpolate %s: maximum tree depth exceeded", path)
			}
			switch n := node.(type) {
			case string:
				return expand(n, path)
			case map[string]any:
				out := make(map[string]any, len(n))
				for k, v := range n {
					p := k
					if path != "" {
						p = path + "." + k
					}
					var err error
					out[k], err = walk(v, p, depth+1)
					if err != nil {
						return nil, err
					}
				}
				return out, nil
			case []any:
				out := make([]any, len(n))
				for i, v := range n {
					var err error
					out[i], err = walk(v, fmt.Sprintf("%s.%d", path, i), depth+1)
					if err != nil {
						return nil, err
					}
				}
				return out, nil
			}
			return node, nil
		}
		expanded, err := walk(root, "", 0)
		if err != nil {
			return err
		}
		for k, v := range expanded.(map[string]any) {
			root[k] = v
		}
		return nil
	}
}
