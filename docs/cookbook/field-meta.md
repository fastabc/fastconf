# Struct field metadata tag

`fastconf:"…"` carries declarative annotations alongside `json` / `yaml` tags. A single line can express several constraints:

```go
type Config struct {
    Level   string `json:"level"   fastconf:"oneof=info|warn|error,default=info,desc=日志级别"`
    Port    int    `json:"port"    fastconf:"required,min=1,max=65535"`
    Secret  string `json:"secret"  fastconf:"secret"`
    Timeout time.Duration `json:"timeout" fastconf:"default=30s"`
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

## Plan dry-run collects every violation

In normal reload the `field-meta` stage fails fast on the first violation; in `mgr.Plan()` it collects everything so a PR-bot can show all missing required fields at once.

## Pairing with WithValidator

Tag-based checks are best for static constraints (non-empty / range / enum). Use `WithValidator(func(*T) error)` for **cross-field** logic the tag cannot express.

## Why not `validate:"…"`?

We deliberately do not run `go-playground/validator` from the core; that lives in the optional `validate/playground` sub-module. The built-in `fastconf:"…"` tag covers the 80% case without pulling a heavy dependency into the root go.mod.
