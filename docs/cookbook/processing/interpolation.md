# 变量插值与跨字段引用

```go
fastconf.WithTransform(transform.Interpolate(transform.InterpolateOptions{Refs: true}))
```

默认通过 `os.LookupEnv` 查找环境变量，也可传入 `Lookup`。

| 语法 | 行为 |
| --- | --- |
| `${NAME}` | 环境变量，未设置时为空串 |
| `${NAME:default}` / `${NAME:-default}` | 未设置或为空时使用默认字面量 |
| `${NAME:?message}` | 未设置或为空时报错，包含配置路径和 message |
| `${server.base_url}` | `Refs: true` 时引用合并树中的标量，支持相同后缀 |
| `$${` | 输出字面量 `${` |

```yaml
server:
  base_url: https://${HOST:example.com}
oauth:
  callback_url: ${server.base_url}/oauth/callback
dsn: postgres://${DB_USER:admin}:${DB_PASS}@${DB_HOST:127.0.0.1}/app
```

引用字符串先递归插值，环和超过 32 层的引用会报错；对象和列表不能作为标量引用。
环境变量的值不二次展开。`Refs: false` 保留含点号的表达式。默认值不能含 `}`。
插值在 merge 之后、secret 和 `default:` 标签之前运行；引用所需默认值请放在 base 层。
展开的环境变量明文需要用 `fc:"secret"` 或 `WithSecretPaths` 标记；引用中的
secret resolver 前缀由随后的 secret 阶段解析。

`EnvSubst` / `EnvSubstWith` 保持兼容：只支持环境变量和 `:-` / `:?`，没有
跨字段引用、`:default` 或 `$${` 转义。需要这些能力时改用 `Interpolate`。
