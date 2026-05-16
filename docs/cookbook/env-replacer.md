# Env key replacer & auto-bind

`pkg/provider.NewEnv("APP_")` uses a double-underscore convention (`APP_DB__POOL` → `db.pool`). For teams used to Viper's single-underscore + replacer pattern, `pkg/provider.NewEnvReplacer` is the drop-in.

## Default double-underscore convention

```go
import (
    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/pkg/provider"
)

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    // APP_DB__POOL → db.pool
    fastconf.WithProvider(provider.NewEnv("APP_")),
)
```

## Single-underscore convention

```go
mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    // APP_DATABASE_DSN → database.dsn
    fastconf.WithProvider(provider.NewEnvReplacer("APP_", provider.DotReplacer)),
)
```

`provider.DotReplacer` lowercases the post-prefix portion and replaces every `_` with `.`. Pass any `provider.EnvKeyReplacer` for non-trivial transformations:

```go
custom := provider.EnvKeyReplacerFunc(func(s string) string {
    // FOO-Xbar → foo.bar
    return strings.ToLower(strings.ReplaceAll(s, "X", "."))
})
fastconf.WithProvider(provider.NewEnvReplacer("APP_", custom))
```

## Mixing replacers

`provider.NewEnv` and `provider.NewEnvReplacer` can co-exist — they advertise distinct provider names (`env:APP_`, `env-replacer:APP_`) so the framework treats them as independent layers. The later one in the option list wins per dotted key.
