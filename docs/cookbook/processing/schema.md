# Generate schemas and examples from structs

`github.com/fastabc/fastconf/schema` provides three functions. Inside a generator
function returning `error`, import `reflect` and `schema` and use:

```go
t := reflect.TypeFor[config.Config]()
js, err := schema.JSONSchema(t)              // Draft 2020-12
if err != nil { return err }
example, err := schema.ExampleYAML(t)        // Annotated YAML
if err != nil { return err }
vars, err := schema.ExampleEnv(t, "APP_", "__")
if err != nil { return err }
// Save or print js, example and vars; see the complete program below.
```

Input must be a struct or a pointer to one. Field names follow JSON tags and
embedding rules. `desc:"…"` takes precedence over `fc:"desc=…"`; defaults come
from `default:"…"` in v1.7.0. All three generators check for removed
`fc:"default=…"` declarations before generating output. As with manager
construction, a declaration on a skipped field or inside a collection is an error.
Use `desc` tags for descriptions because reflection cannot read source comments.

Schemas support primitive scalars, durations (strings with a pattern), `[]byte`
(base64), lists, string-keyed maps and nested structs. `TextUnmarshaler` becomes a
string; `json.Unmarshaler` and interfaces use an unconstrained schema. Recursive
occurrences preserve their object or array types, including array element types.
`required`, numeric `min`/`max`, `oneof` and `secret` map to required properties,
numeric bounds, enums and writeOnly. Objects allow additional properties, matching
the default `UnknownWarn` behavior.
Duration patterns accept signed, fractional and zero literals, and duration
defaults use the same literals, such as `default:"5s"`.

YAML/env comments follow field declaration order. Descriptions are followed by
applicable `(required, secret)` annotations. YAML emits zero values when defaults
are absent and uses flow-style scalar lists. Env emits empty values without
defaults and normally joins lists with commas; elements containing commas,
surrounding whitespace or empty strings use JSON arrays to preserve boundaries.
Fields containing the chosen separator receive an unaddressable-field comment
and are skipped. Prefer `__`, for example `APP_DB__MAX_CONNS`. Applications supply
dynamic map keys themselves. Secret defaults are still emitted; never put real
credentials in tags.

## go generate

Declare a generator in the application's config package:

```go
//go:generate go run ./internal/cmd/genconfig
```

The generator chooses output paths and handles errors:

```go
// internal/cmd/genconfig/main.go (imports omitted)
func main() {
    b, err := schema.ExampleYAML(reflect.TypeFor[config.Config]())
    if err != nil { panic(err) }
    if err := os.WriteFile("config.example.yaml", b, 0o644); err != nil { panic(err) }
}
```

See the [complete schema program](../../../examples/schema/README.md).
