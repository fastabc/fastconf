# Generate configuration artifacts

```sh
GOPROXY=direct go run ./examples/schema
GOPROXY=direct go test ./examples/schema
```

Prints a Draft 2020-12 JSON Schema, annotated YAML example and `.env.example`
from one config struct. See the [recipe](../../docs/cookbook/processing/schema.md)
for writing these artifacts with `go generate`.
