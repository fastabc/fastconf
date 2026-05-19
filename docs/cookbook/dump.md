# Dumping the merged state

When something looks wrong in production, the fastest debug move is to *see the merged config the running process actually has*. FastConf produces a deterministic rendering via `State[T].Dump(format, redactor)` — YAML, JSON, or TOML — over the same merged tree the typed snapshot was built from.

## Library

```go
state := mgr.Snapshot()
b, err := state.Dump(fastconf.DumpYAML, nil) // or DumpJSON / DumpTOML
if err != nil { return err }
_ = os.WriteFile("/tmp/cur.yaml", b, 0o644)
```

Map keys are sorted lexicographically inside every YAML mapping, so two snapshots whose merged values match produce **byte-identical** YAML — diff tools work without flake. JSON uses two-space indent; TOML uses BurntSushi/toml's canonical output.

## `fastconfctl dump --format=yaml`

```bash
fastconfctl dump --dir conf.d --profile prod --format=yaml > /tmp/prod.yaml
fastconfctl dump --dir conf.d --profile dev  --format=yaml > /tmp/dev.yaml
diff -u /tmp/dev.yaml /tmp/prod.yaml
```

The default format is JSON (use `--format=yaml` for the deterministic YAML form).

## Sidecar `/dump` endpoint

`fastconfd` exposes the same artefact over HTTP:

```bash
# YAML (default)
curl -s http://localhost:8650/dump

# JSON
curl -s http://localhost:8650/dump?format=json
```

## Redaction

Pass a redactor to mask secret-tagged paths in place:

```go
b, _ := state.Dump(fastconf.DumpYAML, fastconf.DefaultSecretRedactor)
```

The redactor walks fields tagged `fastconf:"secret"` (or registered via `WithSecretRedactor`) and replaces them with `***REDACTED***` (default) or a custom marker. `/config?redact=true` on the sidecar already uses this path — see the [sidecar recipe](sidecar.md).

## Migration from v0.17

`State[T].MarshalYAML(redactor)` has been removed in v0.18. Replace each call site:

```go
// before
b, err := state.MarshalYAML(redactor)
// after
b, err := state.Dump(fastconf.DumpYAML, redactor)
```

JSON and TOML callers no longer need to reach into `state.Value` and pick their own encoder.
