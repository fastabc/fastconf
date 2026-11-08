# age secret resolver

Independent optional module; Go 1.25 or newer. The root module has no age dependency.

```go
dec, err := age.NewDecrypter(bytes.NewReader(identityFile))
if err != nil { return err }
mgr, err := fastconf.New[Config](ctx, fastconf.WithSecretResolvers(
    fastconf.PrefixSecretResolver("age", age.Prefix, dec),
))
```

Import this adapter as `github.com/fastabc/fastconf/secrets/age`. Identities use
age's text identity format. Values are `age:` followed by standard base64 of a
binary age message, with at most 64 KiB decoded ciphertext. Decryption errors
exclude key material and payloads. Applications own identity storage and rotation.

```sh
printf %s 's3cret' | age -r age1… | base64 | tr -d '\n'
```

Prepend `age:` to that output. On Linux `base64 -w0` also disables line wrapping.
