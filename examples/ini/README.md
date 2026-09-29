# INI configuration

From the repository root, run:

```sh
go run ./examples/ini
go test ./examples/ini
```

Expected output:

```text
name=demo code=00123 port=9090 enabled=true timeout=5s
```

The program embeds [base configuration](conf.d/base/00-app.ini) and a
[prod overlay](conf.d/overlays/prod/50-app.ini). INI values remain strings until
the default typed hooks convert the port, boolean and duration. The string code
keeps its leading zeros. Both layers use strings, so strict merging succeeds.

No INI struct tags or extra options are needed: the default JSON bridge uses
the `json` tags in [main.go](main.go). See the [INI recipe](../../docs/cookbook/ini.md)
for syntax, mixed-format merging and the input-only scope.
