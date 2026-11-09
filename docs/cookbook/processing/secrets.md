# Secret resolution and redaction

`fc:"secret"` masks diagnostic values; it does not decrypt input. Resolvers run
in registration order after transforms and before defaults/typed hooks/decode.
The first recognizer owns each leaf. Resolved paths are automatically masked in
`Map`, `Dump`, `Diff` and `Explain`; `Value`, `Get` and `Unredacted` expose plaintext.
Existing secret tags and `WithSecretPaths` remain useful for unencrypted inputs.

`WithSecretResolvers` appends (ignoring nil); `WithSecretResolver` replaces the list.
`PrefixSecretResolver` strips the prefix before calling an application decrypter;
empty scheme/prefix and nil functions panic as programming errors.

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
origins remain in the chain. Resolved paths are automatically sensitive, including literal map keys containing dots.

## Application-owned KMS client

```go
resolver := fastconf.PrefixSecretResolver("kms", "kms://", func(ctx context.Context, body string) (string, error) {
    blob, err := base64.StdEncoding.DecodeString(body)
    if err != nil { return "", err }
    out, err := kmsClient.Decrypt(ctx, &kms.DecryptInput{CiphertextBlob: blob})
    if err != nil { return "", err }
    return string(out.Plaintext), nil
})
mgr, err := fastconf.New[Config](ctx, fastconf.WithSecretResolvers(resolver))
```

The application owns credentials, clients and safe error messages. Resolver
errors should never include ciphertext, plaintext or authentication material.

## Vault KV v2 and age (v1.6.0)

```go
vr, err := vault.NewResolver(addr, token, vault.WithMount("secret"))
if err != nil { return err }
dec, err := age.NewDecrypter(bytes.NewReader(identityFile))
if err != nil { return err }
mgr, err := fastconf.New[Config](ctx, fastconf.WithSecretResolvers(
    fastconf.PrefixSecretResolver("vault", vault.ResolverPrefix, vr.Resolve),
    fastconf.PrefixSecretResolver("age", age.Prefix, dec),
))
```

`vault` is `providers/vault`; `age` is the independent `secrets/age` module
(Go 1.26+, released under `secrets/age/vX.Y.Z`).
Vault bodies are `app/db#password`, resolved from `/v1/<mount>/data/app/db`.
Static tokens or existing `WithAuth` are supported; a dynamic token retries login once on 403.
Only string, numeric and boolean values are supported.

age values contain `age:` and standard base64 of binary ciphertext (at most 64 KiB).
Create them with:

```sh
printf %s 's3cret' | age -r age1… | base64 | tr -d '\n'
```

Prepend `age:`; Linux can also use `base64 -w0` to remove line wrapping.

## SOPS（应用自持 provider）

SOPS 字段 `ENC[…]` 的数据密钥依赖文件级 `sops:` 元数据，不能独立作为字段解密。
其依赖树较大，因此由应用选择依赖并加载整个文件：

```go
// Application dependency: github.com/getsops/sops/v3/decrypt
type sopsFile struct{ path string }
func (s sopsFile) Name() string { return "sops:" + s.path }
func (s sopsFile) Load(ctx context.Context) (contracts.Snapshot, error) {
    plain, err := decrypt.File(s.path, "yaml")
    if err != nil { return contracts.Snapshot{}, err }
    dec, _ := codec.Lookup("yaml")
    m, err := dec.Decode(plain)
    return contracts.Snapshot{Map: m}, err
}
// fastconf.WithProvider(sopsFile{"secrets.enc.yaml"})
```

这些 provider 输出没有经过 resolver；应用应通过 `fc:"secret"` / `WithSecretPaths` 标记敏感字段。
