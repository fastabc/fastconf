# Env key replacer & namespacing

`providers/env.New("APP_")` defaults to the underscore-to-path convention: every `_` after the prefix becomes a `.` level. Runs of underscores collapse to a single separator, so neither single- nor double-underscore keys produce empty path segments.

| Input env var | With prefix `APP_` | Resulting dotted path |
|---|---|---|
| `APP_DATABASE_DSN`   | strip prefix → `DATABASE_DSN`   | `database.dsn`   |
| `APP_DATABASE__DSN`  | strip prefix → `DATABASE__DSN`  | `database.dsn`   (run collapses) |
| `APP_FEATURE_FLAGS`  | strip prefix → `FEATURE_FLAGS`  | `feature.flags`  |

## Default (single `_` → `.`)

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/providers/env"
)

mgr, err := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    // APP_DATABASE_DSN → database.dsn
    fastconf.WithProvider(env.New("APP_")),
)
if err != nil {
    return err
}
defer mgr.Close()
```

## Preserving `_` in keys (`__` as separator)

When your keys legitimately carry literal underscores (e.g. SCREAMING_SNAKE feature-flag names that the consuming code reads as one identifier), use `DoubleUnderscoreReplacer`:

```go
mgr, err := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    // APP_FEATURE_FLAGS → feature_flags  (single "_" preserved)
    // APP_DATABASE__POOL → database.pool (only "__" introduces a level)
    fastconf.WithProvider(env.New("APP_").
        WithReplacer(env.DoubleUnderscoreReplacer)),
)
if err != nil {
    return err
}
defer mgr.Close()
```

## Custom replacer

Any `env.EnvKeyReplacer` works — useful for bespoke conventions:

```go
custom := env.EnvKeyReplacerFunc(func(s string) string {
    // FOOXbar → foo.bar
    return strings.ToLower(strings.ReplaceAll(s, "X", "."))
})
fastconf.WithProvider(env.New("APP_").WithReplacer(custom))
```

## Namespacing env values under a sub-tree (`At`)

By default env values land at the root of the merged config. `At` places the
whole env-loaded tree under a dotted path. Declare that subtree in a typed
configuration when unknown-field checking is enabled:

```go
fastconf.WithProvider(env.New("APP_").At("config.runtime"))
// APP_DATABASE_DSN → config.runtime.database.dsn
```

`At` is available on both `env.Provider` and `dotenv.Provider`.

## `.env` is a fallback layer

`dotenv.Provider` is intentionally lower priority than process env. Presence
wins, not non-emptiness: if the deployment environment explicitly sets
`APP_PORT=""`, FastConf keeps that empty override and does **not** refill it
from `.env`.

```go
fastconf.WithProvider(
    dotenv.New("APP_", ".env"),
    env.New("APP_"),
)
// APP_PORT="" in the real process env suppresses APP_PORT=8080 from .env
```

The dotenv provider only suppresses keys already present in the process
environment; the env provider loads those process values. Register both to
combine the fallback with deployment overrides.

## Coercion (off by default)

Values remain strings by default. `codec.StringPrimitiveHook` converts them
to built-in scalar field types during typed decoding. If the target is
`map[string]any`, there is no concrete field type to guide
conversion. For eager bool/int/float coercion during provider loading, opt in:

```go
fastconf.WithProvider(env.New("APP_").WithCoerce(true))
```

## Mixing providers

Multiple env providers can use different prefixes and replacers. Names derive
from the prefix (`env:APP_`, `env:LEGACY_`), not the replacer; use distinct
prefixes to keep diagnostic names distinct. Equal-priority providers merge in
registration order, with later values winning at the same path.

For `${VAR:default}` and configuration references, see
[interpolation](../processing/interpolation.md).

## Explicit CLI flags

Use `cliflag.FromStdFlag` for the standard library or the optional pflag adapter's
`FromChanged`. Wrap the result in `cliflag.New` and pass it to `WithProvider`
after document providers. Unchanged flag defaults contribute no keys, so they do
not overwrite files or environment values. Explicit `false` and zero remain real
inputs and survive absent-key `default` tags. Typed hooks convert CLI string
values before field validation.

The [complete pflag program](../../../examples/pflag/README.md) demonstrates a
file host surviving an untouched CLI default, an explicit port override and
`--debug=false`. Run against this checkout with:

```sh
GOPROXY=direct bash tools/modules.sh exec go -C integrations/cli/pflag run ../../../examples/pflag/main.go
```
