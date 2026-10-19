# Files, profiles and patches

Use `Load` for a one-time layered snapshot or `New` for runtime reloads. Both
scan `conf.d` by default; `LoadFile` skips discovery and reads exactly one file.
The complete [basic example](../../../examples/basic/README.md) includes fixtures
and environment overrides.

## Directory layout

```text
conf.d/
  _meta.yaml
  base/00-app.yaml
  overlays/prod/50-app.yaml
  overlays/prod/90-port.patch.json
  regions/eu/50-region.yaml
```

Only files directly inside each selected layer directory are loaded. Normal
YAML, JSON, TOML and INI files merge in filename order. Later scalar values
replace earlier ones; maps merge recursively and lists replace by default.
See [keyed-list merging](../processing/strategic-merge.md) for lists of named objects.

Example `base/00-app.yaml`:

```yaml
server:
  port: 8080
```

Example `overlays/prod/50-app.yaml`:

```yaml
server:
  port: 9090
```

Select prod with `WithProfile(fastconf.Profile{Names: []string{"prod"}})`.
Without the patch below, the merged port is 9090.

## Profile selection

Explicit non-empty `Profile.Names` wins. Otherwise FastConf checks the profile
environment variable (`Profile.Env`, root metadata's `profileEnv`, then
`APP_PROFILE`), `Profile.Default`, then metadata's `defaultProfile`.
Environment profile names are comma-separated. A zero `Profile{}` still uses
these fallbacks.

An overlay without metadata matches its directory name against the active
profile set. An overlay's `_meta.yaml` can instead contain:

```yaml
match: prod & !dev
```

Expressions support `&`, `|`, `!` and parentheses. `Profile.Match` adds a global
filter. Matching overlay directories merge in lexical directory order, then
filename order; `Profile.Names` order does not assign precedence.

## Root metadata and axes

Root `conf.d/_meta.yaml` has a different shape from overlay metadata:

```yaml
spec:
  defaultProfile: prod
  strict: true
  mergeKeys:
    listeners: name
```

`WithStrictMerge(true)` rejects raw type conflicts, unknown file extensions and
a missing base directory. It does not reject unknown struct fields; use
`WithUnknownFields(UnknownError)` for that. Metadata `spec.strict: true` also
enables strict handling. Explicit `WithMergeKeys` entries override metadata.

Add independent region/host dimensions with `WithAxes`:

```go
fastconf.WithAxes(
    fastconf.Axis{Dir: "regions", Env: "DEPLOY_REGION", Priority: 0},
    fastconf.Axis{Dir: "hosts", Env: "DEPLOY_HOST", FromHostname: true, Priority: 100},
)
```

`DEPLOY_REGION=eu` selects `regions/eu/`. A present but empty axis environment
variable disables that axis; an absent variable permits the hostname fallback
when configured. Axes follow profile overlays, ordered by priority then declaration.

## RFC 6902 patches

A `.patch.json` or `.patch.yaml` file contains a JSON Patch operation list and
runs at its position in filename order. Example `90-port.patch.json`:

```json
[
  {"op": "replace", "path": "/server/port", "value": 9443}
]
```

With prod selected, this changes the merged port from 9090 to 9443. Patch paths
are JSON Pointers, not dotted paths. An invalid operation fails the load; reload
keeps the live snapshot. Patches change the candidate tree, not source files.

For class precedence, discovery limits and metadata rules, see the
[runtime contract](../../design/spec.md#layer-order).
