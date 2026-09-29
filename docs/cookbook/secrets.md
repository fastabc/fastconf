# Encrypted secrets (SOPS / age / KMS / Vault transit)

`fc:"secret"` only masks values on display. To decrypt ciphertext stored in
YAML (SOPS / age / AWS KMS / Vault transit / sealed-secrets), install a
`SecretResolver`. It runs between `transform` and `decode`, making plaintext
available to the decoder. Transformers run before resolution;
diagnostic views and audit diffs mask plaintext only when fields are tagged
`fc:"secret"` or matched by `WithSecretPaths`. A resolver does not mark paths
secret automatically.

## Decryption is failure-safe

If `Resolve` returns an error, the reload aborts and the previous `*State[T]` is preserved — the manager never publishes partially-decrypted state.

## Wire a custom resolver

```go
import (
    "context"
    "fmt"

    "github.com/fastabc/fastconf"
)

type sopsResolver struct {
    keys map[string]string // demo only — real impl talks to KMS / age / Vault.
}

func (r *sopsResolver) Recognize(v string) (fastconf.SecretRef, bool) {
    if len(v) > 4 && v[:4] == "enc:" {
        return fastconf.SecretRef{Scheme: "sops", Body: v[4:]}, true
    }
    return fastconf.SecretRef{}, false
}

func (r *sopsResolver) Resolve(_ context.Context, ref fastconf.SecretRef) (string, error) {
    plain, ok := r.keys[ref.Body]
    if !ok {
        return "", fmt.Errorf("unknown key %q", ref.Body)
    }
    return plain, nil
}

mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithSecretResolver(&sopsResolver{
        keys: map[string]string{"abc123": "postgres://prod-master/db"},
    }),
)
```

## YAML

```yaml
database:
  # The resolver walks every leaf string, recognises "enc:..." and
  # replaces it with the plaintext before decode.
  dsn: "enc:abc123"
```

## Provenance and dry-run

`SecretResolver` is invoked by `Manager.Plan(ctx)` too — your PR-bot can fail the build if a key is missing in CI before the change reaches production. Resolved leaves are recorded with `SourceRef.Kind = LayerSecret`, so `State.Explain(path)` reports the secret scheme rather than the original layer.
