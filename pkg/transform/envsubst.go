package transform

import (
	"fmt"
	"os"
	"regexp"
)

// envPattern matches ${VAR}, ${VAR:-default}, or ${VAR:?optional message}.
// Bare $VAR is intentionally NOT matched to avoid clashing with
// bcrypt-style password fields. Capture groups:
//
//	1. name
//	2. operator — "-" (default) or "?" (required); empty for bare ${VAR}
//	3. body    — default value or error message; empty when no operator
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::([-?])([^}]*))?\}`)

// EnvSubst returns a Transformer that walks every string value in the
// tree and substitutes occurrences of ${VAR}, ${VAR:-default}, or
// ${VAR:?required message}. A ${VAR:?...} reference whose variable is
// unset or empty aborts the reload with ErrTransform. EnvSubst is the
// canonical place for ${VAR} interpolation in fastconf; provider/EnvProvider
// and provider/DotEnvProvider deliberately do not expand variables inside
// their values.
func EnvSubst() Transformer { return EnvSubstWith(os.Getenv) }

// EnvSubstWith is like EnvSubst but reads variables through the
// supplied lookup function. Use it to look variables up from sources
// other than os.Getenv — for example, to consult a .env file first and
// fall back to process env, wrap your dotenv lookup in a closure:
//
//	dotenv := map[string]string{ /* parsed once */ }
//	tr := transform.EnvSubstWith(func(name string) string {
//	    if v, ok := dotenv[name]; ok { return v }
//	    return os.Getenv(name)
//	})
func EnvSubstWith(lookup func(string) string) Transformer {
	return TransformerFunc{
		NameStr: "EnvSubst",
		Fn: func(root map[string]any) error {
			var firstErr error
			walkStrings(root, func(s string) string {
				return envPattern.ReplaceAllStringFunc(s, func(match string) string {
					m := envPattern.FindStringSubmatch(match)
					name, op, body := m[1], m[2], m[3]
					v := lookup(name)
					switch op {
					case "?":
						if v == "" && firstErr == nil {
							msg := body
							if msg == "" {
								msg = "variable is required"
							}
							firstErr = fmt.Errorf("%w: EnvSubst: ${%s:?}: %s", ErrTransform, name, msg)
						}
						return v
					case "-":
						if v != "" {
							return v
						}
						return body
					default:
						return v
					}
				})
			})
			return firstErr
		},
	}
}

func walkStrings(node any, fn func(string) string) any {
	switch v := node.(type) {
	case map[string]any:
		for k, vv := range v {
			v[k] = walkStrings(vv, fn)
		}
		return v
	case []any:
		for i, vv := range v {
			v[i] = walkStrings(vv, fn)
		}
		return v
	case string:
		return fn(v)
	default:
		return v
	}
}
