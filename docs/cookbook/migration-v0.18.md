# Migrating to v0.18.0

v0.18.0 is the **first public release**. It collapses every pre-public
flat `WithXxx` variant into bucketed `XxxOptions` structs and renames a
handful of helpers. There is no deprecated alias path; callers must
update at the call site.

## Bucketed Option entries (SPEC-A1)

| Pre-v0.18.0 (deleted)            | v0.18.0                                                                 |
|----------------------------------|--------------------------------------------------------------------------|
| `WithProfile("prod")`            | `WithProfile(ProfileOptions{Single: "prod"})`                            |
| `WithProfiles("eu", "canary")`   | `WithProfile(ProfileOptions{Multi: []string{"eu","canary"}})`            |
| `WithProfileEnv("APP_PROFILE")`  | `WithProfile(ProfileOptions{EnvVar: "APP_PROFILE"})`                     |
| `WithDefaultProfile("dev")`      | `WithProfile(ProfileOptions{Default: "dev"})`                            |
| `WithProfileExpr(expr)`          | `WithProfile(ProfileOptions{Expr: expr})`                                |
| `WithWatch(true)`                | `WithWatch(WatchOptions{Enabled: true})`                                 |
| `WithWatchPaths(p1, p2)`         | `WithWatch(WatchOptions{Enabled: true, Paths: []string{p1, p2}})`        |
| `WithCoalesceQuiet(d)`           | `WithCoalesce(CoalesceOptions{Quiet: d})`                                |
| `WithCoalesceMaxLag(d)`          | `WithCoalesce(CoalesceOptions{MaxLag: d})`                               |
| `WithCoalesceSwapHint(d)`        | `WithCoalesce(CoalesceOptions{SwapHint: d})`                             |
| `WithCoalesceProfile(p)`         | `WithWatch(WatchOptions{CoalesceProfile: p})`                            |

### Composing several knobs

```go
mgr, err := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProfile(fastconf.ProfileOptions{
        EnvVar:  "APP_PROFILE",
        Default: "dev",
        Multi:   []string{"eu", "canary"},
    }),
    fastconf.WithWatch(fastconf.WatchOptions{
        Enabled: true,
        Coalesce: fastconf.CoalesceOptions{
            Quiet:  50 * time.Millisecond,
            MaxLag: 500 * time.Millisecond,
        },
    }),
)
```

Inside a bucketed Option, per-field `Coalesce` values override anything a
`CoalesceProfile` selector already set, so the override order is:
*defaults → `CoalesceProfile` → per-field `Coalesce`*.

## Renames (SPEC-A6, SPEC-A8)

| Pre-v0.18.0 (deleted)        | v0.18.0                            |
|------------------------------|-------------------------------------|
| `WithDefaulterFunc[T](fn)`   | `WithDefaults[T](fn)`              |
| `Sub[T, M](state, fn)`       | `Extract[T, M](state, fn)`         |

## Removed (SPEC-E2)

| Pre-v0.18.0 (deleted)         | Replacement                                                  |
|-------------------------------|---------------------------------------------------------------|
| `provider.NewCLIChanged(m)`   | `provider.NewCLI(m)` — identical behaviour, single name       |

The footgun warning about leaking flag defaults now lives on `NewCLI`'s
godoc. Use `pkg/cliadapter.FromStdFlag` (`flag` package) or
`integrations/cli/pflag.FromChanged` (spf13/pflag) to obtain a
changed-only map.

## Structural type changes (SPEC-A3, SPEC-A4, SPEC-A7)

- `OverlayAxis`, `Transformer`, `MigrationApplier`, `MigrationFunc`, and
  `CodecBridge` are now **root-native** types — no longer aliases for
  `pkg/discovery`, `pkg/transform`, or `internal/options`. Field names
  are identical, so existing struct literals compile without changes.
- `State[T].Diff(other)` now returns `[]DiffEntry` (structured
  per-path records) instead of `[]string`. The CLI line list is still
  available via `fastconf.FormatDiff(entries)`:

  ```go
  entries := mgr.Snapshot().Diff(prev)
  for _, e := range entries {
      switch e.Change {
      case fastconf.DiffAdded:    /* ... */
      case fastconf.DiffRemoved:  /* ... */
      case fastconf.DiffModified: /* ... */
      }
  }
  fmt.Println(strings.Join(fastconf.FormatDiff(entries), "\n"))
  ```

- `PlanResult.Diff` and `DiffEvent.Diff` carry `[]DiffEntry` too.
- `contracts.RawLayer.Priority int` is a new field. Zero defaults to
  `contracts.PriorityGenerator` (= 70), so existing single-layer
  generators keep working. `state.Sources` from a generator now reports
  `LayerGenerator` instead of `LayerProvider`.

## Internal-only (no source impact)

- Go directive relaxed from `go 1.26.2` to `go 1.22`, and the development
  toolchain pin was removed. Consumers on Go 1.22+ can adopt FastConf
  without bumping toolchains.
- `pkg/discovery.ScanOptions.Profile` (single string) has been removed;
  the slice form `Profiles []string` is the only entry point, and a
  one-element slice models the previous single-profile path.
