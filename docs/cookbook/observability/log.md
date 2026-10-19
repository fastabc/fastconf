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
[handler core](#module-boundaries), which ships with the root module.
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
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(h)),
)
if err != nil {
    return err
}
defer mgr.Close()
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
go get github.com/fastabc/fastconf/integrations/log/zerolog@v1.1.0
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
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(zerologadapter.NewHandler(zl, zerologadapter.Options{}))),
)
if err != nil {
    return err
}
defer mgr.Close()
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
go get github.com/fastabc/fastconf/integrations/log/phuslu@v1.1.0
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
mgr, err := fastconf.New[AppConfig](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithLogger(slog.New(phusluadapter.NewHandler(pl, phusluadapter.Options{}))),
)
if err != nil {
    return err
}
defer mgr.Close()
```

This adapter exposes the same `Options` fields as the zerolog adapter:
`Level slog.Leveler`, `AddSource bool`, and `GroupSeparator string`.

Passing a nil logger returns a handler that discards all records without
panicking, which is useful for disabling logging in tests.

---

## Module boundaries

The adapters share `integrations/log/internal/fclog`, a standard-library-only
implementation shipped with the root module. Its internal interfaces are not
application APIs. For another backend implement `slog.Handler`; see Go's
[`log/slog` documentation](https://pkg.go.dev/log/slog).

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

Use `slog.NewJSONHandler` when nested JSON groups are required. The adapters
flatten group names using `GroupSeparator`. To copy JSON output to several
writers, use `io.MultiWriter` with the standard handler.
