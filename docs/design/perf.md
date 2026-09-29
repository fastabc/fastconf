# Performance Notes

## Read-path contract

`Manager.Get()` reads an atomic snapshot pointer: O(1), lock-free, zero
allocations. `tools/bench-guard.sh` checks `BenchmarkGet` in CI with a default
limit of 5 ns/op and 0 allocs/op. Local timings depend on CPU, Go version,
concurrency and instrumentation; they are not a portable latency guarantee.

## Reproduce measurements

Run from the repository root with a clean workspace and record the commit,
Go version, OS, architecture and `GOMAXPROCS` alongside the samples:

```bash
go version
go env GOOS GOARCH
GOWORK=off GOMAXPROCS=4 go test . -run '^$' \
  -bench '^(BenchmarkGetWarmState|BenchmarkReloadNoop|BenchmarkReloadCommitSmall|BenchmarkReloadLarge|BenchmarkStateMapCold)$' \
  -benchmem -count=5
bash tools/bench-guard.sh
bash tools/profile-reload.sh /tmp/fastconf-profiles
```

`BenchmarkReloadNoop` reloads unchanged files. `BenchmarkReloadCommitSmall`
changes an override on each iteration and verifies generation advances.
`BenchmarkReloadLarge` uses 256 file layers; `BenchmarkReloadLargeIncremental`
changes one layer. `BenchmarkStateMapCold` measures a snapshot's first masked
map view. Keep race-detector timings separate from performance samples.

## Input fingerprint

After assembly, file-only inputs are fingerprinted using file contents,
paths, priority, profile, codec, root metadata and codec registry generation.
An equal fingerprint skips merge through hashing. Files are still read on
each reload: neither size nor mtime determines freshness.

The shortcut is disabled by providers, generators, overrides, transforms,
secret resolvers, custom typed hooks, validators, policies, observers,
`Defaulter`, or custom JSON/YAML/text encoding methods anywhere in `T`.
These hooks may depend on external state. Rollback clears the fingerprint;
failed reloads do not update it. Plans always execute their pipeline.

## Retained memory

Each manager caches at most 512 decoded file layers. A key includes file
path, codec, content hash and codec registry generation. Trees are cloned
before use; failures are not cached. Eviction is FIFO, bounded by entry
count rather than bytes, so retained memory depends on document sizes.

Snapshots lazily cache one immutable JSON tree for diagnostics. Each returned
view is detached, including custom redactor inputs. `Get` does not create
this tree. History and application-held snapshots extend its lifetime.
`Explain` copies the origin chain and checks type metadata and secret path
patterns; origin containers with secret descendants are masked as a whole.

The final typed value is hashed after decode/defaults whenever the pipeline
runs. Subscriber equality avoids unnecessary callbacks. RFC 6902 patching
works on a copy of the merged map, retaining untouched leaf types.

## Resource soak

```bash
GOWORK=off GOMAXPROCS=4 FASTCONF_SOAK_DURATION=5m \
  go test -mod=readonly -race -count=1 -timeout=10m \
  -run '^TestResourceSoak$' -v .
```

The soak covers reloads, plans, file caching, history, observers and shutdown.
The manual resource-soak workflow runs the platform matrix. Record actual
platform results for each candidate; earlier runs do not validate a new one.
