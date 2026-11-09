# Resolver chains, rotation and safe diagnostics

```sh
GOPROXY=direct go run ./examples/secrets
GOPROXY=direct go test -race ./examples/secrets
```

[main.go](main.go) combines an application-owned `PrefixSecretResolver` with
`vault.NewResolver`, wrapped using `vault.ResolverPrefix` and `Resolve`.
A local HTTP-client fixture supplies a Vault KV v2 response; no network listener,
Vault account or real credentials are needed. In production pass your own client
and authentication options; references use `ref+vault://path#key`.

```text
runtime-ready=true diagnostics=map[api_key:***REDACTED*** password:***REDACTED***]
preview changes=1 live-preserved=true
~ api_key : ***REDACTED*** -> ***REDACTED***
resolution failed: preserved=true
```

`WithSecretResolvers` appends resolvers in order; the first recognizer wins for
one leaf. `WithSecretResolver` replaces the whole chain and nil clears it.
Resolved fields are automatically masked in `Map`, `Dump`, `Diff` and `Explain`,
including `Load[any]` / `New[any]` configurations. `fc:"secret"` and
`WithSecretPaths` still cover plaintext credentials without resolver references.

`Get` / `Value` return plaintext for application use and must remain read-only.
Use `Unredacted()` only for an explicit plaintext operation; do not log these
values. The example logs diagnostic views, previews a rotation with `Plan` and
`PlanResult.Err()`, then reloads. A resolution failure leaves the previous
snapshot intact. `Plan` does not reserve the inputs used by a later `Reload`.

An equal-value switch to a secret reference can publish new redaction metadata
without changing `Hash` or calling value subscribers. Generation/history and
`Committed` observers still reflect that publication.

See the separate [age example](../age/README.md) for real local encryption and
decryption, and the [secret cookbook](../../docs/cookbook/processing/secrets.md).
