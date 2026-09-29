# Typed decoder hooks

`encoding/json` cannot natively unmarshal a YAML string like `"30s"` into a `time.Duration` field — it refuses the string→int64 conversion. Typed hooks plug a pre-decode rewrite into the pipeline that converts the string into a form the selected decoder (JSON or YAML) accepts.

## Default

`DurationHook` ships in `codec.DefaultTypedHooks()` and is installed automatically. Any `time.Duration` field decodes from a Go duration string:

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

The pipeline stage is still present but no hook applies — `time.Duration` once again requires explicit numeric nanoseconds.

## Custom hooks

Add a hook for a named scalar type (enum, custom int, …):

```go
import "github.com/fastabc/fastconf/codec"

type Mood int

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
    return raw, nil
}

mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithTypedHook(moodHook{}),
)
```

## Where hooks apply

The plan follows the fields the selected decoder actually fills: embedded structs (untagged anonymous structs for the JSON decoder, `yaml:",inline"` for `WithDecoder(YAML)`), pointers, slice/array elements and map values. Recursive types (`type Node struct{ Next *Node }`) are supported; the walk follows the finite input tree.

## URL / IP / Regex

`codec.URLHook` is opt-in and fills `url.URL` / `*url.URL` fields: a hook result whose type is the destination type itself is assigned directly after decoding, because `url.URL` has no JSON/YAML string form. `codec.IPHook` and `codec.RegexHook` only validate the string; `net.IP` decodes it natively, while `*regexp.Regexp` fields are best modelled as `string` and compiled on first use.

## Cost

The hook plan is built **once** at `New()` via a reflect pass over `*T`; the per-reload `typed-hooks` stage is a tree walk with no further reflection. `BenchmarkGet` is unaffected.
