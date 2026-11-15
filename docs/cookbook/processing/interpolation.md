# Interpolation and cross-field references

```go
fastconf.WithTransform(transform.Interpolate(transform.InterpolateOptions{Refs: true}))
```

Lookup defaults to `os.LookupEnv`; supply `Lookup` for a custom environment.
The [complete processing example](../../../examples/config-processing/README.md)
uses a deterministic lookup and tests required-value failures and literal insertion.

| Syntax | Behavior |
| --- | --- |
| `${NAME}` | Environment value, or an empty string when unset |
| `${NAME:default}` / `${NAME:-default}` | Literal fallback when unset or empty |
| `${NAME:?message}` | Fail when unset or empty, including the config path and message |
| `${server.base_url}` | With `Refs: true`, reference a scalar in the merged tree; the same suffixes apply |
| `$${` | Emit literal `${` |

```yaml
server:
  base_url: https://${HOST:example.com}
oauth:
  callback_url: ${server.base_url}/oauth/callback
dsn: postgres://${DB_USER:admin}:${DB_PASS}@${DB_HOST:127.0.0.1}/app
```

Referenced strings expand recursively; cycles and references deeper than 32
levels fail. Objects and lists cannot be scalar references. Environment values
are inserted literally without another expansion. `Refs: false` preserves dotted
expressions. Fallback literals cannot contain `}`.

Interpolation runs after merge, before secret resolution and absent-key defaults.
Place defaults needed by references in a base layer. Mark environment-expanded
secrets with `fc:"secret"` or `WithSecretPaths`; resolver prefixes introduced by
references are processed by the subsequent secret stage.

`EnvSubst`/`EnvSubstWith` remain compatible with their original environment-only
syntax, including `:-` and `:?`. Use `Interpolate` for cross-field references,
`:default` fallbacks or `$${` escapes.
