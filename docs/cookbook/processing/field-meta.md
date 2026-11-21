# Struct tags: defaults, validation and secrets

`json` / `yaml` tags name input keys, `default:"…"` supplies literals for absent
keys, and `fc:"…"` adds FastConf constraints and diagnostic redaction to exported
struct fields. They work automatically with `LoadFile[T]` (added in v1.2.0),
`Load[T]` and `New[T]`, without enabling a separate validator. Use matching JSON
and YAML names when both bridges are used.

Separate `fc` directives with commas and enumeration values with `|`. For example:

```go
type Config struct {
    Level  string `json:"level" yaml:"level" default:"info" fc:"oneof=info|warn|error,desc=Log level"`
    Port   int    `json:"port" yaml:"port" fc:"required,min=1,max=65535"`
    Token  string `json:"token" yaml:"token" fc:"required,secret"`
}
```

| Tag | Effect | Stage |
|---------|--------|-------|
| `default:"…"` | inject a literal when the input key is absent; see [absent-key defaults](#absent-key-defaults-with-default-tags) | `defaults` (before `typed-hooks` and `decode`) |
| `fc:"required"` | reject a Go zero value; does not check whether an input key was present | `field-meta` (before `validate`) |
| `fc:"min=N"` / `fc:"max=N"` | inclusive bounds for numeric fields; not string length or collection size | `field-meta` |
| `fc:"oneof=a\|b\|c"` | match the field's `fmt.Sprint` representation; strings and numeric scalars can be enumerated | `field-meta` |
| Pointer fields | `required` checks for nil; `min` / `max` / `oneof` check the pointed-to value and skip nil pointers | `field-meta` |
| `fc:"secret"` | mark for diagnostic redaction | display only — see [secrets.md](secrets.md) for *decryption* |
| `fc:"desc=…"` | human-readable source annotation; ignored by the runtime | none |

`fc:"default=…"` was removed in v1.7.0. A field that still declares it, including
one inside a collection element, fails construction with an error naming the
field; see [migrating from `fc` defaults](#migrating-from-fc-defaults).

The `fc` parser does not support escaping commas or `|` inside directive values.
Unknown directives and invalid `min` / `max` numbers are currently ignored,
so a misspelling such as `requried` does not enable validation. An invalid
`default:"…"` literal fails construction.

## Load a tagged struct

Given the struct above and `app.yaml`:

```yaml
port: 8080
token: demo-token
```

Use this integration fragment with imports `context`,
`github.com/fastabc/fastconf` and `github.com/fastabc/fastconf/providers/source`:

```go
state, err := fastconf.Load[Config](context.Background(),
    fastconf.WithProvider(source.NewFile("app.yaml")),
    fastconf.WithUnknownFields(fastconf.UnknownError),
)
if err != nil {
    return err // inside a function returning error
}
cfg := state.Value() // Level="info", Port=8080, Token="demo-token"; read-only
_ = cfg
```

See the [single-file guide](../../getting-started/load.md) for a complete program
and the default `conf.d` directory behavior. Missing `level` receives `info`;
`level: debug`, `port: 70000`, or a missing/empty `token` fails with `ErrInvalid`.
On reload, a rejected candidate leaves the previous snapshot active.

Execution order is absent-key default injection → typed hooks → decode →
`Defaulter.Defaults()` → field-tag validation → `WithValidate` callbacks.
Consequently, `default:"8080" fc:"required"` accepts an absent integer field after
injecting it; it does not require the caller to supply that key. Directive order
within a tag does not change these stages.

`secret` masks diagnostic `Map`, `Dump`, `Diff` and `Explain` views. `Value()`
and `Manager.Get()` still contain plaintext, as do explicit `Unredacted()` views.
Printing or JSON-marshaling the config yourself does not apply FastConf redaction.

## Durations and computed defaults

`default:"30s"` is converted by the same typed hooks as `timeout: 30s` in
configuration, so durations, lists and named primitives use readable literals.
Numeric `min` / `max` on a duration compare nanoseconds.

For computed defaults, or to replace an explicit zero, implement `Defaulter`
(import `time`):

```go
type TimedConfig struct {
    Timeout time.Duration `json:"timeout" yaml:"timeout"`
}

func (c *TimedConfig) Defaults() {
    if c.Timeout == 0 {
        c.Timeout = 30 * time.Second
    }
}
```

This replaces an explicit zero as well as a missing value. Use a base layer as
described below when zero must remain meaningful.

## Missing, zero and null

`default:"…"` fills only absent input keys. For bool/int/string fields declared
with `default:"true"`, `default:"3"` and `default:"default"` respectively:

| Input | `default:"…"` | `Defaulter` replacing zero | Base layer without default tags |
|---|---|---|---|
| Missing key in all inputs | Default value | Default value | Inherit base value if supplied |
| Explicit false / 0 / empty string | Preserved | Replaced | Preserved |
| Explicit null | Preserved; decodes to zero | Replaced | Replaces base; scalar becomes zero |

The table assumes non-strict merge, a fresh scalar struct and no custom decoder
or hook. Strict merge may reject a null type conflict earlier. `required` also
checks zero values, so it rejects a legitimate false or 0. A pointer can preserve
an explicit false, but nil alone cannot distinguish absent input from null.
`Defaulter.Defaults()` runs after decoding and may replace any zero value.

For switches and limits, use the absent-key tag below, or put defaults in `conf.d/base/00-defaults.yaml`:

```yaml
enabled: true
retries: 3
label: default
```

Then `conf.d/overlays/prod/10-app.yaml` can explicitly override them:

```yaml
enabled: false
retries: 0
label: ""
```

```go
type Config struct {
    Enabled bool   `json:"enabled" yaml:"enabled"`
    Retries int    `json:"retries" yaml:"retries"`
    Label   string `json:"label" yaml:"label"`
}
// Load with WithDir("conf.d") and
// WithProfile(fastconf.Profile{Names: []string{"prod"}}).
```

Check any `Defaults()` method so it does not replace these zero values.
To reject null for these top-level fields, add this option (import `fmt`):

```go
fastconf.WithTransform(func(tree map[string]any) error {
    for _, key := range []string{"enabled", "retries", "label"} {
        if v, present := tree[key]; present && v == nil {
            return fmt.Errorf("%s must not be null", key)
        }
    }
    return nil
}),
```

Nested fields require checking their nested map. This explicit application
policy preserves the existing default-tag behavior for other users.

## Migrating from `fc` defaults

Up to v1.6.0, `fc:"default=…"` filled a Go zero value after decoding. Choose the
replacement by the behavior each field needs:

| Previous tag | Keep explicit false / 0 / ""? | Replacement |
|---|---|---|
| `fc:"default=8080"` | Yes (usually intended) | `default:"8080"` |
| `fc:"default=8080"` | No, zero must become 8080 | A `Defaults()` method that checks for zero |
| `fc:"default=30000000000"` on a `time.Duration` | Either | `default:"30s"` or `Defaults()` |
| `fc:"min=1,default=9"` | Either | `default:"9" fc:"min=1"`, or `Defaults()` and `fc:"min=1"` |
| Default on a collection element field | — | `Defaults()` iterating the elements |

Keep the other `fc` directives (`required`, `min`, `max`, `oneof`, `secret`,
`desc`) in the `fc` tag. `default:"…"` also records `LayerDefault` origins for
`Explain` and appears in generated schemas and examples.

## Unknown fields

Use `WithUnknownFields(UnknownError)` for production struct configurations.
The default `UnknownWarn` logs decoder findings, suppresses repeated identical
warnings, and continues decoding. Its default logger discards output. To receive
warnings during a permissive migration, configure both options:

```go
fastconf.WithUnknownFields(fastconf.UnknownWarn),
fastconf.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))),
```

Import `log/slog` and `os`. Warnings are not failures on `Errors()` and do not
promise a complete list of unknown keys. Map targets accept arbitrary keys;
custom unmarshaling must enforce its own rules.

## Collection elements

Exported fields promoted from unexported embedded structs also participate
(fixed in v1.3.0). Unexported non-embedded fields are ignored.
Nested structs and existing struct pointers
are traversed; a nil parent pointer is skipped and is not allocated to apply
child defaults. Put `fc:"required"` on the parent pointer if it must exist.
On a scalar pointer, `required` checks for nil. Numeric bounds and `oneof`
dereference non-nil pointers, including multiple pointer levels; nil pointers
skip these optional constraints.

Validation tags (`required`, `min`, `max`, `oneof`) traverse nested structs and
pointers, but do not traverse slice, array, or map elements. A tag on the
collection field itself still applies to that field. For example, `Items []Item`
does not apply `Item.Port`'s `fc:"required"` tag to each item, and a
`default:"…"` tag on an element field fails construction. Implement a `Defaulter`
for element defaults and `WithValidate` for element checks. Secret redaction uses
a separate walker and is unaffected by this limitation.

## Plan dry-run collects every violation

In a normal reload, any field metadata violations reject the candidate with
`ErrInvalid`. `mgr.Plan(ctx)` collects these violations in `Validators` without
rejecting the preview; inspect each report's `Err` before approving it. See the
[runnable reload example](../../../examples/reload/README.md).

## Pairing with WithValidate

Tag-based checks are best for static constraints (non-empty / range / enum). Use `WithValidate(func(*T) error)` for **cross-field** logic the tag cannot express.

## Why not `validate:"…"`?

We deliberately do not run `go-playground/validator` from the core; that lives in the optional `validate/playground` sub-module. The built-in tag supports common constraints without that dependency.

## Absent-key defaults with `default` tags

```go
Port int `json:"port" default:"8080"`
Timeout time.Duration `json:"timeout" default:"5s"`
Enabled bool `json:"enabled" default:"true"`
Hosts []string `json:"hosts" default:"a,b"`
```

| Input | `default:"…"` |
| --- | --- |
| Missing key | Inject a literal, then apply typed hooks/decode |
| Explicit false / 0 / empty string | Preserve |
| Explicit null | Preserve; decode as a zero value |
| Explain | `LayerDefault`, `default://tag` |

Use `default` tags for switches, timeouts and lists, and `Defaulter` for computed
defaults. Defaults are injected after transforms and secrets, so they do not
participate in interpolation or secret resolution. Invalid literals fail
construction.

Presence follows the selected decoder: JSON prefers exact field names, then the
first declared case-insensitive match; YAML uses case-sensitive YAML names only.
Tags belonging to the other bridge do not affect injection. Multiple JSON input
aliases merging into one object preserve supplied children, including zero values.
Defaults inside collection elements are unsupported. Missing or null nullable
parents are not allocated; defaults apply to their children only when the parent
object exists. Ordinary nested structs can create missing object paths.
See the [processing example](../../../examples/config-processing/README.md).
