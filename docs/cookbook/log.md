# JSON and structured logging with zerolog and phuslu/log

FastConf accepts a standard-library `*slog.Logger` through `WithLogger`.
The caller chooses the `slog.Handler` and logging backend; the root module's
logging code depends only on `log/slog`.

For JSON Lines output, choose one of these three options:

| Use case | Recommended option | Logging dependency |
|---|---|---|
| JSON Lines with standard field names | `slog.NewJSONHandler` | Standard library only |
| An application already using zerolog | The `integrations/log/zerolog` module | zerolog |
| An application already using phuslu/log | The `integrations/log/phuslu` module | phuslu/log |

Each adapter has its own `go.mod`. Both share the standard-library-only
[`fclog` core](#shared-handler-core-fclog), which ships with the root module.
Importing the zerolog adapter does not pull in phuslu/log, or vice versa.

---

## Option A: standard-library JSON handler

```go
import (
    "log/slog"
    "os"

    "github.com/fastabc/fastconf"
)

h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
cfg, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(h)),
)
```

Example output:

```json
{"time":"2026-05-15T12:34:56Z","level":"INFO","msg":"fastconf reload swap","reason":"watcher","generation":7,"layers":5}
```

To use zerolog's default message field name, `message`, instead of `msg`:

```go
opts := &slog.HandlerOptions{
    ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
        switch a.Key {
        case slog.MessageKey: a.Key = "message"
        }
        return a
    },
}
h := slog.NewJSONHandler(os.Stderr, opts)
```

---

## Option B: zerolog adapter

```bash
go get github.com/fastabc/fastconf/integrations/log/zerolog@latest
```

```go
import (
    "log/slog"
    "os"

    "github.com/fastabc/fastconf"
    zerologadapter "github.com/fastabc/fastconf/integrations/log/zerolog"
    "github.com/rs/zerolog"
)

zl := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.InfoLevel)
cfg, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(zerologadapter.NewHandler(zl, zerologadapter.Options{}))),
)
```

`Options` fields:

| Field | Type | Meaning |
|---|---|---|
| `Level` | `slog.Leveler` | Optional slog threshold. The default, `nil`, delegates filtering to zerolog |
| `AddSource` | `bool` | Include the call site as a `source` field containing `file:line` |
| `GroupSeparator` | `string` | Separator for nested `slog.Group` key prefixes; defaults to `.` |

Use a `*slog.LevelVar` to change the slog threshold at runtime. The backend
must also allow the requested level:

```go
lv := new(slog.LevelVar)
lv.Set(slog.LevelInfo)
h := zerologadapter.NewHandler(zl.Level(zerolog.DebugLevel), zerologadapter.Options{Level: lv})

// Allow FastConf debug logs without changing zerolog's global level.
lv.Set(slog.LevelDebug)
```

The adapter captures the zerolog logger by value. Changing the original
logger variable later does not change the handler's backend configuration.

---

## Option C: phuslu/log adapter

```bash
go get github.com/fastabc/fastconf/integrations/log/phuslu@latest
```

```go
import (
    "log/slog"
    "os"

    "github.com/fastabc/fastconf"
    phusluadapter "github.com/fastabc/fastconf/integrations/log/phuslu"
    plog "github.com/phuslu/log"
)

pl := &plog.Logger{
    Level:      plog.InfoLevel,
    TimeFormat: "2006-01-02T15:04:05.999Z07:00",
    Writer:     plog.IOWriter{Writer: os.Stderr},
}
cfg, _ := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(phusluadapter.NewHandler(pl, phusluadapter.Options{}))),
)
```

This adapter exposes the same `Options` fields as the zerolog adapter:
`Level slog.Leveler`, `AddSource bool`, and `GroupSeparator string`.

Passing a nil logger returns a handler that discards all records without
panicking, which is useful for disabling logging in tests.

---

## Choosing an option

| Use case | Choice |
|---|---|
| JSON output without an existing logging backend | **A**: standard library |
| Existing zerolog application | **B**: zerolog adapter |
| Existing phuslu/log application | **C**: phuslu/log adapter |
| Another backend, such as zap, logrus, or charmbracelet/log | Implement a `slog.Handler` in your application, following the adapter examples |

The backend dependencies remain in the optional adapter modules. Selecting an
adapter does not add a logging backend requirement to the root `go.mod`.

---

## Shared handler core: `fclog`

`integrations/log/internal/fclog` implements the shared `slog.Handler`
behavior. It ships with the root module and depends only on the standard
library, which CI checks. Go's `internal` import boundary restricts it to
packages under `integrations/log`; application code should use an adapter.

Module relationships:

```text
github.com/fastabc/fastconf                  root module
└── integrations/log/internal/fclog         shared handler; standard library only
.../integrations/log/phuslu                 root module + phuslu/log
.../integrations/log/zerolog                root module + zerolog
```

Responsibilities:

| Shared in `fclog` | Implemented by each adapter |
|---|---|
| Map `slog.Level` to Trace/Debug/Info/Warn/Error | Map the shared level to a backend level (`plevel` / `zlevel`) |
| Apply `Options.Level`, including a mutable `slog.Leveler` | `Backend.Enabled`: check the backend's threshold |
| Preserve `WithAttrs` / `WithGroup` scope and flatten groups into key prefixes | `Backend.Event`: create a record, or return nil if it is dropped |
| Dispatch `slog.Value` by kind, recognize errors, and resolve `LogValuer` values | `Event`: forward `Str`, `Int64`, `Err`, `Any`, and other fields to the backend |
| Render `AddSource` as `file:line`; discard records when the backend is nil | |

Adapters implement two small interfaces:

```go
type Backend interface {
    Enabled(lvl Level) bool
    Event(lvl Level) Event // nil means the backend dropped the record
}

type Event interface {
    Str(key, val string)
    Int64(key string, val int64)
    Uint64(key string, val uint64)
    Float64(key string, val float64)
    Bool(key string, val bool)
    Dur(key string, val time.Duration)
    Time(key string, val time.Time)
    Err(key string, err error)
    Any(key string, val any)
    Msg(msg string)
}
```

Each adapter's `Options` has the same fields as `fclog.Options` and converts
directly to it. Shared behavior tests live in
`integrations/log/internal/logtest`.

To add a backend inside this repository, create an independent module under
`integrations/log/<backend>`, implement `Backend` and `Event`, and require the
root `github.com/fastabc/fastconf` module. External applications cannot import
this internal package; implement a `slog.Handler` as described in
[Choosing an option](#choosing-an-option).

---

## Field mapping

Both adapters share the `slog.Attr` mapping implemented in `internal/fclog`:

| `slog.Value` kind | zerolog method | phuslu/log method |
|---|---|---|
| `KindString` | `Str(k, v)` | `Str(k, v)` |
| `KindInt64` | `Int64(k, v)` | `Int64(k, v)` |
| `KindUint64` | `Uint64(k, v)` | `Uint64(k, v)` |
| `KindFloat64` | `Float64(k, v)` | `Float64(k, v)` |
| `KindBool` | `Bool(k, v)` | `Bool(k, v)` |
| `KindDuration` | `Dur(k, v)` | `Dur(k, v)` |
| `KindTime` | `Time(k, v)` | `Time(k, v)` |
| `KindAny` containing an error | `AnErr(k, err)` | `AnErr(k, err)` |
| Other `KindAny` values | `Interface(k, v)` | `Any(k, v)` |
| `KindGroup` | Flattened keys joined with `GroupSeparator`, such as `stage.name` | Same as zerolog |

---

## FAQ

- **Why are the adapters separate modules?** Each adapter isolates its backend dependencies, so users of the root module do not need either logging library. This follows the same module boundary used by `observability/otel`, `cue`, and `policy/opa`.
- **Can I use both adapters?** Yes. To send records to both, use a `slog.Handler` that delegates to both handlers. To write the same JSON output to multiple destinations, pass an `io.MultiWriter` to `slog.NewJSONHandler`.
- **Why do groups use dotted keys instead of nested JSON objects?** Both adapters use the shared key-prefix mapping to preserve group scope. Use `slog.NewJSONHandler` if you need nested JSON objects.
