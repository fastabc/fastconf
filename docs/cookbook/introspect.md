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

## Why no `mgr.GetString("a.b")` shortcut?

FastConf intentionally does **not** add string-path read methods on
`*Manager`. They would add work to the lock-free typed read path. If you
really need string-path lookups in a hot path, switch `T` to `map[string]any`
(and accept losing field-level compile checks).

## Dumping the merged state

When something looks wrong in production, the fastest debug move is to *see the merged config the running process actually has*. FastConf produces a deterministic rendering via `State[T].Dump(format)` — YAML, JSON, or TOML — over the same merged tree the typed snapshot was built from, with secrets masked.

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
curl -s http://localhost:8650/dump

# JSON
curl -s http://localhost:8650/dump?format=json
```

### Redaction

`Dump` always masks fields tagged `fc:"secret"` and paths matched by `WithSecretPaths`, showing `***REDACTED***` (or whatever `WithRedactor` returns). Plaintext is an explicit, greppable call:

```go
b, _ := state.Unredacted().Dump(fastconf.YAML)
``` Map-typed configurations have no struct tags, so they rely on path patterns:

```go
mgr, _ := fastconf.New[map[string]any](ctx, fastconf.WithSecretPaths("**.password", "db.dsn"))
```

The sidecar's `/dump` and `/config` use this path — see the [sidecar recipe](sidecar.md).

### Prefer Dump Over Custom Encoding

Use `State[T].Dump(format)` for every operator-facing export path:

```go
b, err := state.Dump(fastconf.YAML)
```

JSON and TOML callers do not need to reach into `state.Value()` and pick their
own encoder; `Dump` preserves FastConf's redaction and deterministic tree
shape.
