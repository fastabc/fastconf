# Inspecting and dumping a snapshot (`Map` / `Dump` / `Explain`)

FastConf's hot read path is `mgr.Get() *T` — strong-typed, zero-alloc, no
string paths. For debug endpoints, CLI dumps or DI helpers you sometimes want
the opposite: a generic tree. That lives on `State[T]`, and every view there
masks secrets by default.

| API | Returns | When to use |
|-----|---------|-------------|
| `state.Map()` | fresh `map[string]any`, secrets masked | debug endpoints, diff tools, dotted lookups |
| `state.Dump(fastconf.YAML)` | deterministic YAML / JSON / TOML bytes, secrets masked | operator dumps, golden files |
| `state.Explain("db.dsn")` | the layers that wrote a path, values masked for secrets | "where did this value come from?" (needs `WithProvenance`) |
| `state.Unredacted().Map()` / `.Dump(f)` | the same without masking | code that must see plaintext — greppable on purpose |
| `&state.Value().Database` | typed pointer into the snapshot | strong-typed DI for a sub-struct (read-only) |

## Examples

```go
state := mgr.Snapshot()

// dotted lookup on the masked tree
dsn, _ := confmap.GetDotted(state.Map(), "database.dsn") // "***REDACTED***" if tagged fc:"secret"

// sub-tree as a fresh map (no shared mutation)
db, _ := state.Map()["database"].(map[string]any)

// strong-typed sub-tree pointer (read-only)
dbCfg := &state.Value().Database
```

Secrets are the fields tagged `fc:"secret"` plus any `WithSecretPaths`
patterns; `WithRedactor` changes how they display.

## Dumping the merged state

Use `State[T].Dump(format)` to inspect the configuration the process is using.
It renders YAML, JSON or TOML from the final typed snapshot, with secrets masked.
Defaults are included; keys dropped during typed decoding are absent. Diagnostic
paths follow JSON tags.

### Library

```go
state := mgr.Snapshot()
b, err := state.Dump(fastconf.YAML) // or JSON / TOML; secrets masked
if err != nil { return err }
_ = os.WriteFile("/tmp/cur.yaml", b, 0o644)
```

Map keys are sorted lexicographically inside every YAML mapping, so two snapshots whose merged values match produce **byte-identical** YAML — diff tools work without flake. JSON uses two-space indent; TOML uses BurntSushi/toml's canonical output.

### `fastconfctl dump --format=yaml`

```bash
fastconfctl dump --dir conf.d --profile prod --format=yaml > /tmp/prod.yaml
fastconfctl dump --dir conf.d --profile dev  --format=yaml > /tmp/dev.yaml
diff -u /tmp/dev.yaml /tmp/prod.yaml
```

The default format is JSON (use `--format=yaml` for the deterministic YAML form).

### Sidecar `/dump` endpoint

`fastconfd` exposes the same artefact over HTTP:

```bash
# YAML (default)
curl -s -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/dump

# JSON
curl -s -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/dump?format=json
```

### Redaction

`Dump` always masks fields tagged `fc:"secret"` and paths matched by `WithSecretPaths`, showing `***REDACTED***` (or whatever `WithRedactor` returns). Plaintext is an explicit, greppable call:

```go
b, _ := state.Unredacted().Dump(fastconf.YAML)
```

Map-typed configurations have no struct tags, so they rely on path patterns:

```go
mgr, _ := fastconf.New[map[string]any](ctx, fastconf.WithSecretPaths("**.password", "db.dsn"))
```

The sidecar's `/dump` and `/config` use this path — see the [sidecar recipe](sidecar.md).

## History and rollback

Configure `WithHistory(4)` before creating a manager. `mgr.History()` is nil
when history is disabled; `List()` returns retained previous snapshots, oldest
first, excluding the live snapshot. Pass a retained snapshot to `Rollback`:

```go
history := mgr.History()
if history != nil {
    states := history.List()
    if len(states) > 0 {
        if err := history.Rollback(states[len(states)-1]); err != nil {
            return err
        }
    }
}
```

Rollback publishes a new generation with a new cause timestamp and reason
`rollback`; it does not rerun validation or edit source files. A later reload
reads the sources again. See the complete [reload example](../../examples/reload/README.md).
