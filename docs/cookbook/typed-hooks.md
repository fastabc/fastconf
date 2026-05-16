# Typed decoder hooks

`encoding/json` cannot natively unmarshal a YAML string like `"30s"` into a `time.Duration` field — it refuses the string→int64 conversion. Typed hooks plug a pre-decode rewrite into the pipeline that converts the string into the wire form the JSON decoder accepts.

## Default

`DurationHook` ships in `pkg/decoder.DefaultTypedHooks()` and is installed automatically. Any `time.Duration` field decodes from a Go duration string:

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
import "github.com/fastabc/fastconf/pkg/decoder"

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

## Why not URL / IP / Regex by default?

`url.URL`, `net.IP`, and `*regexp.Regexp` have **no native JSON wire form**, so a pre-decode rewrite cannot land them in the right shape. Future work may add a *post*-decode reflection injector for these; until then, model such fields as `string` and parse on first use, or write a focused `TypedHook` that emits a structured form your custom `UnmarshalJSON` accepts.

## Cost

The hook plan is built **once** at `New()` via a reflect pass over `*T`; the per-reload `typed-hooks` stage is a tree walk with no further reflection. `BenchmarkGet` is unaffected.
