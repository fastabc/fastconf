# Struct tag `fc`: defaults, validation and secrets

`fc:"…"` adds FastConf metadata to exported struct fields. `json` / `yaml`
tags name input keys; `fc` describes defaults, constraints and diagnostic
redaction. It works automatically with `LoadFile[T]` (new in the current checkout),
`Load[T]` and `New[T]`, without enabling
a separate validator. Use matching JSON and YAML names when both bridges are used.

Separate directives with commas and enumeration values with `|`. For example:

```go
type Config struct {
    Level  string `json:"level" yaml:"level" fc:"default=info,oneof=info|warn|error,desc=日志级别"`
    Port   int    `json:"port" yaml:"port" fc:"required,min=1,max=65535"`
    Token  string `json:"token" yaml:"token" fc:"required,secret"`
}
```

| Tag key | Effect | Stage |
|---------|--------|-------|
| `required` | reject a Go zero value; does not check whether an input key was present | `field-meta` (before `validate`) |
| `min=N` / `max=N` | inclusive bounds for numeric fields; not string length or collection size | `field-meta` |
| `oneof=a\|b\|c` | match the field's `fmt.Sprint` representation; strings and numeric scalars can be enumerated | `field-meta` |
| `default=…` | populate zero-valued string, bool, integer or float fields after decode | `decode` |
| `secret` | mark for diagnostic redaction | display only — see [secrets.md](secrets.md) for *decryption* |
| `desc=…` | human-readable source annotation; ignored by the runtime | none |

The parsers do not support escaping commas or `|` inside directive values.
Unknown directives and invalid `min` / `max` numbers are currently ignored,
so a misspelling such as `requried` does not enable validation. An invalid or
overflowing `default` returns `ErrDecode` when applied to a zero-valued field.

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

See the [single-file guide](../getting-started/load.md) for a complete program
and the default `conf.d` directory behavior. Missing `level` receives `info`;
`level: debug`, `port: 70000`, or a missing/empty `token` fails with `ErrInvalid`.
On reload, a rejected candidate leaves the previous snapshot active.

Execution order is decode → tag defaults → `Defaulter.Defaults()` → field-tag
validation → `WithValidate` callbacks. Consequently, `required,default=8080`
accepts an absent integer field after filling it; it does not require the caller
to supply that key. Directive order within a tag does not change these stages.

`secret` masks diagnostic `Map`, `Dump`, `Diff` and `Explain` views. `Value()`
and `Manager.Get()` still contain plaintext, as do explicit `Unredacted()` views.
Printing or JSON-marshaling the config yourself does not apply FastConf redaction.

## Duration and other default types

Tag defaults support primitive kinds, not arbitrary parsed types, pointers or
collections. `time.Duration` has underlying kind `int64`: a tag default must be
an integer number of nanoseconds (`default=30000000000` for 30 seconds).
`fc:"default=30s"` fails when the default is applied. The typed hook that accepts
`timeout: 30s` in configuration runs before decoding and does not parse tag defaults.

For readable duration defaults or computed values, implement `Defaulter`
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

This still replaces an explicit zero. Use a base layer as described below when
zero must remain meaningful. Numeric `min` / `max` on a duration also use nanoseconds.

## Missing, zero and null

`default` means **fill a Go zero value after decoding**. It does not mean “fill
only an absent input key”. For ordinary bool/int/string fields tagged with
`default=true`, `default=3` and `default=default` respectively:

| Input | Tag default | Base layer without default tags |
|---|---|---|
| Missing overlay key | Default value | Inherit base value |
| Explicit false / 0 / empty string | Replaced by default | Preserved |
| Explicit null | Decodes to zero, then default | Replaces base; scalar becomes zero |

The table assumes non-strict merge, a fresh scalar struct and no custom decoder
or hook. Strict merge may reject a null type conflict earlier. `required` also
checks zero values, so it rejects a legitimate false or 0. A pointer can preserve
an explicit false, but nil alone cannot distinguish absent input from null.
`Defaulter.Defaults()` runs after tag defaults and may also replace zero values.

For switches and limits, put defaults in `conf.d/base/00-defaults.yaml`:

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

Remove `fc:"default=..."` from these fields and check any `Defaults()` method.
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

Only exported fields participate. Nested structs and existing struct pointers
are traversed; a nil parent pointer is skipped and is not allocated to apply
child defaults. Put `fc:"required"` on the parent pointer if it must exist.
On a scalar pointer, `required` checks for nil, while numeric bounds do not
dereference it. Use `WithValidate` for constraints on the pointed-to scalar.

Defaults and validation tags (`default`, `required`, `min`, `max`, `oneof`)
traverse nested structs and pointers, but do not traverse slice, array, or map
elements. A tag on the collection field itself still applies to that field.
For example, `Items []Item` does not apply `Item.Port`'s `fc:"default=80"`
or `fc:"required"` tag to each item. Implement a `Defaulter` for element defaults
and `WithValidate` for element checks. Secret redaction uses a separate walker
and is unaffected by this limitation.

## Plan dry-run collects every violation

In a normal reload, any field metadata violations reject the candidate with
`ErrInvalid`. `mgr.Plan(ctx)` collects these violations in `Validators` without
rejecting the preview; inspect each report's `Err` before approving it. See the
[runnable reload example](../../examples/reload/README.md).

## Pairing with WithValidate

Tag-based checks are best for static constraints (non-empty / range / enum). Use `WithValidate(func(*T) error)` for **cross-field** logic the tag cannot express.

## Why not `validate:"…"`?

We deliberately do not run `go-playground/validator` from the core; that lives in the optional `validate/playground` sub-module. The built-in tag supports common constraints without that dependency.
