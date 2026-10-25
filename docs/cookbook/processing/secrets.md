# Secret resolution and redaction

`fc:"secret"` masks diagnostic values; it does not decrypt input. For encrypted
values, implement a `SecretResolver` around the application's chosen decryption
client. FastConf has no built-in SOPS, age, KMS or Vault transit decrypter. The resolver
runs after `transform` and before typed hooks and decode. Transformers run before resolution;
diagnostic views and audit diffs mask plaintext only when fields are tagged
`fc:"secret"` or matched by `WithSecretPaths`. A resolver does not mark paths
secret automatically.

## Path patterns

`WithSecretPaths("*.password", "**.token")` uses dot-separated segments:
`*` matches one map key or list index, and `**` matches zero or more segments.
Matching a container masks the whole subtree.

There is no escape syntax for a literal dot in a key. For example,
`WithSecretPaths("annotations.*")` masks values under `annotations`, including
the literal key `example.com/token`; `annotations.example.com/token` instead
describes nested keys. Provenance stores dotted paths without key boundaries,
so `Explain` masks every plausible wildcard match and can hide additional values.
Fields containing secrets that implement `json.Marshaler` or
`encoding.TextMarshaler` are masked as a whole when their output hides the
original field structure.

`Map` and `Dump` use JSON output names even with the YAML decode bridge.
`Explain` follows input aliases as well, including secret fields omitted by
`json:"-"` and fields flattened by `yaml:",inline"`. Input-path matching is
conservative and may mask additional historical values.

## Decryption is failure-safe

If `Resolve` returns an error, the reload aborts and the previous `*State[T]` is preserved — the manager never publishes partially-decrypted state.

## Wire a custom resolver

The following map-backed resolver only demonstrates the interface; it does not
parse a SOPS document or perform cryptography. Import paths for real clients
and key ownership belong to the application.

```go
import (
    "context"
    "fmt"

    "github.com/fastabc/fastconf"
)

type exampleResolver struct {
    keys map[string]string // demo only — real impl talks to KMS / age / Vault.
}

func (r *exampleResolver) Recognize(v string) (fastconf.SecretRef, bool) {
    if len(v) > 4 && v[:4] == "enc:" {
        return fastconf.SecretRef{Scheme: "demo", Body: v[4:]}, true
    }
    return fastconf.SecretRef{}, false
}

func (r *exampleResolver) Resolve(_ context.Context, ref fastconf.SecretRef) (string, error) {
    plain, ok := r.keys[ref.Body]
    if !ok {
        return "", fmt.Errorf("unknown key %q", ref.Body)
    }
    return plain, nil
}

mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithSecretPaths("database.dsn"),
    fastconf.WithSecretResolver(&exampleResolver{
        keys: map[string]string{"abc123": "postgres://prod-master/db"},
    }),
)
if err != nil {
    return err
}
defer mgr.Close()
```

## YAML

```yaml
database:
  # The resolver walks every leaf string, recognises "enc:..." and
  # replaces it with the plaintext before decode.
  dsn: "enc:abc123"
```

## Provenance and dry-run

`Plan(ctx)` also invokes the resolver; missing keys can fail a preview.
With provenance enabled, resolved leaves add an origin with
`SourceRef.Kind = LayerSecret` and path `secret://<scheme>`. Existing layer
origins remain in the chain. Resolution does not mark the path sensitive: tag
the destination or configure `WithSecretPaths` before exposing diagnostics.
