# Typed decoder hooks

`encoding/json` cannot natively unmarshal a YAML string like `"30s"` into a `time.Duration` field — it refuses the string→int64 conversion. Typed hooks plug a pre-decode rewrite into the pipeline that converts the string into a form the selected decoder (JSON or YAML) accepts.

## Default

`codec.DefaultTypedHooks()` installs `DurationHook` and `StringPrimitiveHook`.
Duration strings become `time.Duration`; strings for built-in bool/integer/float
fields convert to their destination types. Named scalar types need a matching
hook. For example:

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

## URL / IP / Regex

`codec.URLHook` is opt-in and fills `url.URL` / `*url.URL` fields: a hook result whose type is the destination type itself is assigned directly after decoding, because `url.URL` has no JSON/YAML string form. `codec.IPHook` validates and canonicalizes IP strings; `codec.RegexHook`
validates regex syntax. The destination types use the selected decoder's native
text-unmarshaling support.

## Cost

The field plan is built once during `New` or `Load`. Each pipeline execution
walks the input tree; assignment of direct typed results such as URLs also uses
reflection. These operations do not run on the `Get()` read path.
