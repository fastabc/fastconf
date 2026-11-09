# Defaults, interpolation and typed KV values

Run from the repository root (Go 1.25+, no services required):

```sh
GOPROXY=direct go run ./examples/config-processing
GOPROXY=direct go test -race ./examples/config-processing
```

[main.go](main.go) loads [config.yaml](config.yaml) through `Load`, applies
`transform.Interpolate`, then lets FastConf's default typed hooks decode the
result. Expected output:

```text
enabled=false retries=0 timeout=5s default-source=true
level=3 feature=true hosts=[api-a api-b] ports=[8080 9090] regions=[eu us]
callback=https://example.test/callback owner=demo literal=${NOT_EXPANDED}
```

- `default:"…"` fills absent keys: explicit `false` and `0` survive. A missing
  `time.Duration` receives `5s`; `Explain` attributes it to `LayerDefault`.
  The older `fc:"default=…"` fills decoded zero values instead.
- Default hooks accept comma lists, JSON array strings, contiguous index maps,
  and named numeric/boolean types. `[]byte` retains base64 wire semantics;
  custom unmarshalers remain in control of their input. Applications using
  `codec.BuildTypedHookPlan` directly opt in with `codec.WithListCoercion()`
  and append `codec.NamedPrimitiveHook{}` after their application hooks.
- `${HOST:example.test}` supplies an environment fallback; `${OWNER:?…}`
  requires a value; `Refs: true` enables `${server.url}`; `$${…}` escapes it.
  The deterministic `Lookup` fixture can be omitted to use `os.LookupEnv`.
  Lookup values are inserted literally, without a second expansion.

Interpolation runs before secret resolution and absent-key defaults. References
therefore address merged input values, not defaults or decrypted values added
later. Cycles, missing references and required values reject loading. The test
also exercises required-value rejection and literal lookup content.

See [field metadata](../../docs/cookbook/processing/field-meta.md),
[interpolation](../../docs/cookbook/processing/interpolation.md) and
[schema generation](../schema/README.md).
