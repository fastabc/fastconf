# Struct field metadata tag

`fc:"…"` carries declarative annotations alongside `json` / `yaml` tags. A single line can express several constraints:

```go
type Config struct {
    Level   string `json:"level"   fc:"oneof=info|warn|error,default=info,desc=日志级别"`
    Port    int    `json:"port"    fc:"required,min=1,max=65535"`
    Secret  string `json:"secret"  fc:"secret"`
    Timeout time.Duration `json:"timeout" fc:"default=30s"`
}
```

| Tag key | Effect | Stage |
|---------|--------|-------|
| `required` | reload aborts when the field is the zero value | `field-meta` (before `validate`) |
| `min=N` / `max=N` | numeric bounds (inclusive) | `field-meta` |
| `oneof=a\|b\|c` | string enumeration | `field-meta` |
| `default=…` | populate zero values (after decode) | `decode` |
| `secret` | mark for `SecretRedactor` | display only — see [secrets.md](secrets.md) for *decryption* |
| `desc=…` | human-readable description (used by `fastconfgen`) | doc-time |

## Collection elements

Defaults and validation tags (`default`, `required`, `min`, `max`, `oneof`)
traverse nested structs and pointers, but do not traverse slice, array, or map
elements. A tag on the collection field itself still applies to that field.
For example, `Items []Item` does not apply `Item.Port`'s `fc:"default=80"`
or `fc:"required"` tag to each item. Implement a `Defaulter` for element defaults
and `WithValidate` for element checks. Secret redaction uses a separate walker
and is unaffected by this limitation.

## Plan dry-run collects every violation

In normal reload the `field-meta` stage fails fast on the first violation; in `mgr.Plan(ctx)` it collects everything so a PR-bot can show all missing required fields at once.

## Pairing with WithValidate

Tag-based checks are best for static constraints (non-empty / range / enum). Use `WithValidate(func(*T) error)` for **cross-field** logic the tag cannot express.

## Why not `validate:"…"`?

We deliberately do not run `go-playground/validator` from the core; that lives in the optional `validate/playground` sub-module. The built-in `fc:"…"` tag covers the 80% case without pulling a heavy dependency into the root go.mod.
