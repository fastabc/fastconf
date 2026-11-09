# Typed decoder hooks

`encoding/json` cannot natively unmarshal a YAML string like `"30s"` into a `time.Duration` field — it refuses the string→int64 conversion. Typed hooks plug a pre-decode rewrite into the pipeline that converts the string into a form the selected decoder (JSON or YAML) accepts.

## Default

`codec.DefaultTypedHooks()` installs `DurationHook` and `StringPrimitiveHook`.
Duration strings become `time.Duration`; strings for built-in bool/integer/float
fields convert to their destination types. Since the post-v1.5.0 additions,
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

## KV 字符串叶子

默认 hooks 支持标量列表和没有自定义反序列化器的命名基础类型。用户 hook
优先于 `NamedPrimitiveHook`；`codec.DefaultTypedHooks()` 的返回值不变。
单独使用 `codec.BuildTypedHookPlan` 时，通过 `codec.WithListCoercion()` 开启列表转换。

| 输入 | 目标 | 结果 |
| --- | --- | --- |
| `"a, b"` | `[]string` | `[a b]` |
| `'["a,b","c"]'` | `[]string` | `[a,b c]` |
| `{"0":"80","1":"443"}` | `[]int` | `[80 443]` |
| `"3"` | `type Level int` | `3` |

元素含逗号时使用 JSON 数组；空串生成空列表。下标必须从 0 连续且没有前导零。
`[]byte` 保持 base64 语义，字节数组也不参与列表转换。
`WithoutDefaultTypedHooks()` 同时关闭默认列表转换和命名基础类型兜底。
Azure App Configuration 的 JSON ContentType 可用 `azappconfig.WithJSONValues()`
选择性解码（支持 `application/json; charset=utf-8`）。
