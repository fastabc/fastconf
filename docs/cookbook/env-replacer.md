# Env key replacer & namespacing

`pkg/provider.NewEnv("APP_")` defaults to the Viper / Spring Boot relaxed-binding convention: every `_` after the prefix becomes a `.` level. Runs of underscores collapse to a single separator, so neither single- nor double-underscore keys produce empty path segments.

| Input env var | With prefix `APP_` | Resulting dotted path |
|---|---|---|
| `APP_DATABASE_DSN`   | strip prefix → `DATABASE_DSN`   | `database.dsn`   |
| `APP_DATABASE__DSN`  | strip prefix → `DATABASE__DSN`  | `database.dsn`   (run collapses) |
| `APP_FEATURE_FLAGS`  | strip prefix → `FEATURE_FLAGS`  | `feature.flags`  |

## Default (single `_` → `.`)

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/provider"
)

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    // APP_DATABASE_DSN → database.dsn
    fastconf.WithProvider(provider.NewEnv("APP_")),
)
```

## Preserving `_` in keys (`__` as separator)

When your keys legitimately carry literal underscores (e.g. SCREAMING_SNAKE feature-flag names that the consuming code reads as one identifier), use `DoubleUnderscoreReplacer`:

```go
mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    // APP_FEATURE_FLAGS → feature_flags  (single "_" preserved)
    // APP_DATABASE__POOL → database.pool (only "__" introduces a level)
    fastconf.WithProvider(provider.NewEnv("APP_").
        WithReplacer(provider.DoubleUnderscoreReplacer)),
)
```

## Custom replacer

Any `provider.EnvKeyReplacer` works — useful for bespoke conventions:

```go
custom := provider.EnvKeyReplacerFunc(func(s string) string {
    // FOO-Xbar → foo.bar
    return strings.ToLower(strings.ReplaceAll(s, "X", "."))
})
fastconf.WithProvider(provider.NewEnv("APP_").WithReplacer(custom))
```

`provider.NewEnvReplacer(prefix, replacer)` is a thin shortcut for `provider.NewEnv(prefix).WithReplacer(replacer)`.

## Namespacing env values under a sub-tree (`At`)

By default env values land at the root of the merged config. Use `At` to graft the whole env-loaded tree under a dotted path — useful when you want to keep operator-injected runtime values out of the main schema:

```go
fastconf.WithProvider(provider.NewEnv("APP_").At("config.runtime"))
// APP_DATABASE_DSN → config.runtime.database.dsn
```

`At` is available on both `EnvProvider` and `DotEnvProvider`.

## Coercion (off by default)

Values are kept verbatim as strings; the typed-decode chain (`pkg/decoder.StringPrimitiveHook` in `DefaultTypedHooks`) converts them to the destination field type at `*T` decode time. If you consume the merged map directly (without the typed-decode hook chain) and want eager bool/int/float coercion at Load time, opt in:

```go
fastconf.WithProvider(provider.NewEnv("APP_").WithCoerce(true))
```

## Mixing providers

Multiple env providers with different prefixes / replacers can co-exist — they advertise distinct names (`env:APP_`, `env:LEGACY_` …) so the framework treats them as independent layers. The later one in the option list wins per dotted key when priorities tie.
