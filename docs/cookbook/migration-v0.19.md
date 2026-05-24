# Migration guide: v0.19 Subscribe semantics

v0.19 changes `fastconf.Subscribe` from "fire on every committed reload" to
"fire only when the extracted value changes".

The new default compares the values returned by `extract` with
`reflect.DeepEqual` after dereferencing pointers. Two nil values do not fire;
nil to non-nil and non-nil to nil transitions always fire.

## Remove caller-side equality filters

Before v0.19, callers usually filtered inside the callback:

```go
fastconf.Subscribe(mgr,
    func(c *Config) *Database { return &c.Database },
    func(old, neu *Database) {
        if old != nil && old.DSN == neu.DSN && old.Pool == neu.Pool {
            return
        }
        reconnect(neu)
    },
)
```

After v0.19, keep the callback focused on the side effect:

```go
fastconf.Subscribe(mgr,
    func(c *Config) *Database { return &c.Database },
    func(_, neu *Database) {
        reconnect(neu)
    },
)
```

## Keep fire-on-every-reload behavior

If the callback is an audit, mirror, heartbeat, or other side effect that must
run for every committed reload, install a comparator that always returns false:

```go
fastconf.Subscribe(mgr,
    func(c *Config) *Config { return c },
    func(_, neu *Config) {
        mirror(neu)
    },
    fastconf.WithEqual(func(_, _ *Config) bool { return false }),
)
```

## Ignore noisy fields

Use `WithEqual` when only part of a subtree should drive the callback:

```go
fastconf.Subscribe(mgr,
    func(c *Config) *Database { return &c.Database },
    func(_, neu *Database) {
        reconnect(neu)
    },
    fastconf.WithEqual(func(a, b *Database) bool {
        return a.DSN == b.DSN
    }),
)
```

## Checklist

- Remove inline equality checks from callbacks that only react to config value
  changes.
- Add `WithEqual(func(_, _ *T) bool { return false })` for callbacks that must
  run on every reload.
- Prefer a custom comparator for large subtrees or fields with expected noise.
- Keep panic-free comparators; FastConf recovers panics and publishes them on
  `Manager.Errors()`, but the affected subscriber invocation is skipped.
