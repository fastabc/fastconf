# Migration guides within v0

Archived notes for moving between v0 releases. To move to v1, use
[migration-v1.md](migration-v1.md).

## Migration guide: v0.18 to v0.20

This guide records the v0.18 API moves retained by the current v0 line. The
compatibility shims are intentionally small; update imports and call sites
before adopting the v1 proposal in the architecture plan.

### Imports and options

- Replace `policy/cue` with `cue/policy` and
  `validate/cue/cuelang` with `cue/cuelang`.
- Replace `providers/s3events` with `providers/s3/s3events`.
- Use grouped `WithProfile`, `WithWatch`, and `WithCoalesce` options.
- Use `WithDefaults`, `Extract`, and `contracts.Generator`.

### Subscribe callbacks

`Subscribe` is diff-aware by default. If a callback must run on every
successful commit, opt in explicitly:

```go
fastconf.Subscribe(mgr, extract, callback,
    fastconf.WithEqual(func(_, _ *Value) bool { return false }))
```

### Metadata tags

Use `fc` for FastConf metadata and keep JSON names explicit when the default
JSON bridge is used:

```go
Port int `json:"port" fc:"required,min=1,max=65535"`
```

### Diagnostics and providers

Provider failures satisfy `errors.Is(err, fastconf.ErrProvider)`. Use
`State.Explain`/`LookupStrict` for provenance. Bound Sources preserve revision
and file watch paths; `State.Origins` returns an isolated read view.

## Migration guide: v0.19 Subscribe semantics

v0.19 changes `fastconf.Subscribe` from "fire on every committed reload" to
"fire only when the extracted value changes".

The new default compares the values returned by `extract` with
`reflect.DeepEqual` after dereferencing pointers. Two nil values do not fire;
nil to non-nil and non-nil to nil transitions always fire.

### Remove caller-side equality filters

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

### React to every committed reload

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

### Ignore noisy fields

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

### Checklist

- Remove inline equality checks from callbacks that only react to config value
  changes.
- Add `WithEqual(func(_, _ *T) bool { return false })` for callbacks that must
  run on every commit while the extracted values are non-nil. Unchanged reloads
  do not commit; use `ReloadFinished` observer events to track those too.
- Prefer a custom comparator for large subtrees or fields with expected noise.
- Keep panic-free comparators; FastConf recovers panics and publishes them on
  `Manager.Errors()`, but the affected subscriber invocation is skipped.
