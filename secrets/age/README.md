# age secret resolver

Independent optional module; Go 1.26 or newer. The root module has no age dependency.

This module is new after v1.5.0 and is not included in the root module archive.
Its release tag is `secrets/age/vX.Y.Z`, coordinated with the root `vX.Y.Z` tag.
After publication, install it with `go get github.com/fastabc/fastconf/secrets/age@vX.Y.Z`.
Before publication, run the [local example](../../examples/age/README.md) through
the repository's temporary workspace.

```go
dec, err := age.NewDecrypter(bytes.NewReader(identityFile))
if err != nil { return err }
mgr, err := fastconf.New[Config](ctx, fastconf.WithSecretResolvers(
    fastconf.PrefixSecretResolver("age", age.Prefix, dec),
))
if err != nil { return err }
defer mgr.Close()
// Keep the manager alive while the application uses its configuration.
```

Import this adapter as `github.com/fastabc/fastconf/secrets/age`. Identities use
age's text identity format. Values are `age:` followed by standard base64 of a
binary age message, with at most 64 KiB decoded ciphertext. Decryption errors
exclude key material and payloads. Applications own identity storage and rotation.

```sh
printf %s 's3cret' | age -r age1… | base64 | tr -d '\n'
```

Prepend `age:` to that output. On Linux `base64 -w0` also disables line wrapping.
