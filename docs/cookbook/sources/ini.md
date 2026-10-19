# INI input

INI is a built-in document decoder. Files ending in `.ini`
(case-insensitive) participate in normal base/overlay discovery, merging,
validation, provenance and file watching. No registration import is needed.
See the [complete example](../../../examples/ini/README.md).

```ini
name = demo
code = 00123

[server]
port = 8080
enabled = true
timeout = 5s
```

The decoder produces root keys `name`, `code`, and a `server` map. All leaf
values are strings. Default typed hooks convert them according to the target
field: `int`, `bool`, `time.Duration`, etc. A string field retains `00123`.
A `map[string]any` target has no concrete field types to guide conversion, so
its INI values remain strings. Disabling default typed hooks also disables their
conversions; named custom scalar types may require a custom hook.

## Syntax and mapping

| Input | Behavior |
|---|---|
| Keys before any section, or under `[DEFAULT]` | Root map entries; no implicit inheritance into other sections |
| `[server]` | A child map under the literal key `server` |
| Section/key case and dots | Preserved; `[server.http]` is one literal section name, not nested maps |
| Repeated section / key | Sections combine; the last value for a key wins |
| Root key and section with the same name | Decode error, including when the root key appears later under `[DEFAULT]` |
| `key=value` or `key: value` | String value; surrounding unquoted whitespace is trimmed |
| `key=` | Empty string, not null |
| Blank lines and full-line `;` / `#` comments | Ignored |
| Inline `;` / `#` comments | Recognized when preceded by a space; use double quotes for values containing such text |
| Double-quoted values | Outer quotes removed; `\"` becomes `"`, comment characters inside remain literal |
| Trailing backslash | Literal; backslash line continuation is disabled |
| UTF-8 BOM and CRLF | Accepted |
| `a,b`, `${NAME}`, `%(name)s` | Literal strings; no automatic lists, environment expansion or interpolation |

Parsing uses [go-ini](https://github.com/go-ini/ini/tree/v1.67.3). FastConf takes
each section's own keys and their raw parsed values, avoiding its interpolation,
inheritance and type-conversion helpers. Malformed sections, missing delimiters
and other parser errors abort loading. Standard struct `json` tags apply by
default; `WithDecodeFormat(YAML)` selects `yaml` tags instead. There is no
`ini` struct-tag bridge.

## Mixed formats and strict merging

INI can overlay YAML, JSON and TOML. With normal merging, an INI string can
replace a numeric value and then be converted to the target type.

`WithStrictMerge(true)` checks the raw tree **before** typed hooks. A YAML
integer `port: 80` and INI string `port=8080` therefore conflict with `ErrMerge`.
For strict mixed-format merging, use matching raw types, such as YAML
`port: "80"`; alternatively keep those fields in INI layers throughout.
Do not globally coerce INI scalars just to bypass this check: identifiers and
strings with leading zeros would change meaning.

## Other sources and output

`source.NewFile("app.ini")` and `source.NewBytes("inline", "ini", data)` reuse
the decoder. HTTP sources can select it with hints such as `text/ini`,
`application/ini`, or an explicitly supplied `codec.For("ini")` decoder.
Generic `text/plain` and `application/octet-stream` are not treated as INI.

Support is for input only. `State.Dump` continues to offer YAML/JSON/TOML, and
JSON Patch documents remain YAML/JSON. There is no `fastconf.INI` format constant,
INI dump, comment-preserving round trip, or change to `_meta.yaml` syntax.
Extensions `.cfg` and `.conf` remain unregistered.

Previously ignored `.ini` files in configuration directories now load. Review
existing directory contents when upgrading. A syntax error during reload
preserves the previous snapshot and is reported as `ErrDecode`.
