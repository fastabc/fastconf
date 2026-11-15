# Typed decoder hooks

`encoding/json` cannot natively unmarshal a YAML string like `"30s"` into a `time.Duration` field — it refuses the string→int64 conversion. Typed hooks plug a pre-decode rewrite into the pipeline that converts the string into a form the selected decoder (JSON or YAML) accepts.

## Default

`codec.DefaultTypedHooks()` installs `DurationHook` and `StringPrimitiveHook`.
Duration strings become `time.Duration`; strings for built-in bool/integer/float
fields convert to their destination types. Since v1.6.0,
Manager also coerces scalar lists and installs `NamedPrimitiveHook` after
application hooks for named numeric/boolean types without custom unmarshalers.
For example:

```yaml
server:
  readTimeout: "1500ms"
  shutdownGrace: "30s"
```

```go
type Cfg struct {
    Server struct {
        ReadTimeout   time.Duration `json:"readTimeout"`
        ShutdownGrace time.Duration `json:"shutdownGrace"`
    } `json:"server"`
}
```

## Disable defaults

```go
fastconf.WithoutDefaultTypedHooks()
```

No default hooks apply, but explicitly registered custom hooks still run. The
JSON bridge then requires numeric nanoseconds for `time.Duration`; the YAML
bridge can decode duration strings through its own decoder.

## Custom hooks

Add a hook for a named scalar type (enum, custom int, …):

```go
import (
    "fmt"
    "reflect"
)

type Mood int

type MoodConfig struct {
    Mood Mood `json:"mood"`
}

type moodHook struct{}

func (moodHook) Match(t reflect.Type) bool {
    return t == reflect.TypeOf(Mood(0))
}
func (moodHook) Convert(raw any) (any, error) {
    if s, ok := raw.(string); ok {
        switch s {
        case "happy": return 1, nil
        case "sad":   return -1, nil
        }
    }
    return nil, fmt.Errorf("invalid mood: %v", raw)
}

mgr, err := fastconf.New[MoodConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithTypedHook(moodHook{}),
)
if err != nil {
    return err
}
defer mgr.Close()
```

## Where hooks apply

The plan follows the fields the selected decoder actually fills: embedded structs (untagged anonymous structs for the JSON decoder, `yaml:",inline"` for `WithDecodeFormat(YAML)`), pointers, slice/array elements and map values. Recursive types (`type Node struct{ Next *Node }`) are supported; the walk follows the finite input tree.

The JSON bridge accepts case-insensitive field names during hook conversion and
direct assignment as well as decoding; YAML-only aliases do not trigger conversion.
The YAML bridge uses only its case-sensitive
YAML field names; JSON aliases and fields tagged `yaml:"-"` are not converted.
Standalone `codec.BuildTypedHookPlan` retains its legacy exact input aliases;
pass `codec.WithJSONFields()` to select only JSON decoder fields, as Manager does.

## URL / IP / Regex

`codec.URLHook` is opt-in and fills `url.URL` / `*url.URL` fields: a hook result whose type is the destination type itself is assigned directly after decoding, because `url.URL` has no JSON/YAML string form. `codec.IPHook` validates and canonicalizes IP strings; `codec.RegexHook`
validates regex syntax. The destination types use the selected decoder's native
text-unmarshaling support.

## Cost

The field plan is built once during `New` or `Load`. Each pipeline execution
walks the input tree; assignment of direct typed results such as URLs also uses
reflection. These operations do not run on the `Get()` read path.

## String-valued KV leaves

Default manager hooks support scalar lists and named numeric/boolean types
without custom unmarshalers. Application hooks run before `NamedPrimitiveHook`;
`codec.DefaultTypedHooks()` itself is unchanged. Standalone
`codec.BuildTypedHookPlan` callers enable lists with `codec.WithListCoercion()`.

| Input | Destination | Result |
| --- | --- | --- |
| `"a, b"` | `[]string` | `[a b]` |
| `'["a,b","c"]'` | `[]string` | `[a,b c]` |
| `{"0":"80","1":"443"}` | `[]int` | `[80 443]` |
| `"3"` | `type Level int` | `3` |

Use JSON arrays for elements containing commas. An empty string produces an empty
list. Index keys must be contiguous from zero without leading zeros. `[]byte`
retains base64 semantics; byte arrays also skip list coercion.
`WithoutDefaultTypedHooks()` disables list coercion and the named-primitive
fallback as well. Azure App Configuration can selectively decode JSON settings
with `azappconfig.WithJSONValues()`, including `application/json; charset=utf-8`.
See the [processing example](../../../examples/config-processing/README.md) and
[typed Parameter Store example](../../../examples/parameterstore/README.md).
