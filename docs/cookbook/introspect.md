# Sub-tree introspection (`AllKeys` / `AllSettings` / `Sub` / `Sub`)

FastConf's hot read path is `mgr.Get() *T` — strong-typed, zero-alloc, no string paths. Sometimes you need the **opposite**: a flat dotted-key view for debug endpoints, CLI dumps, or DI helpers. That lives on `State[T]`.

| API | Returns | When to use |
|-----|---------|-------------|
| `state.Introspect().Keys()` | sorted `[]string` of dotted leaves | CLI listings, completion |
| `state.Introspect().Settings()` | fresh `map[string]any` with dotted keys | `/dump` JSON, diff tools |
| `state.Introspect().At("database")` | fresh `map[string]any`, prefix stripped | inject a sub-tree into a sub-module |
| `Sub[T,M](state, extract)` | live `*M` pointing into `state.Value` | strong-typed DI for a sub-struct |

The flat dotted-key view is **lazy** — built on first access via an `atomic.Pointer` cache on the State, so normal reload paths pay nothing.

## Examples

```go
state := mgr.Snapshot()

// dotted keys, sorted
for _, k := range state.Introspect().Keys() {
    fmt.Println(k)
}

// dotted-key map, freshly allocated
all := state.Introspect().Settings()
fmt.Println(all["server.addr"])

// sub-tree as a fresh map (no shared mutation)
db := state.Introspect().At("database")
fmt.Println(db["dsn"]) // "postgres://..."

// strong-typed sub-tree pointer (read-only, aliases state.Value)
type DBView struct{ DSN string `json:"dsn"` }
dbv := fastconf.Sub(state, func(c *AppConfig) *DBView {
    return &c.Database
})
```

## Why no `mgr.GetString("a.b")` shortcut?

FastConf intentionally does **not** add string-path read methods on `*Manager`. They would break the 0.73 ns/op zero-alloc contract. If you really need string-path lookups in a hot path, switch `T` to `map[string]any` (and accept losing field-level compile checks).
