# Dumping the merged state to YAML

When something looks wrong in production, the fastest debug move is to *see the merged config the running process actually has*. FastConf produces a deterministic YAML rendering via `State.MarshalYAML`.

## Library

```go
state := mgr.Snapshot()
b, err := state.MarshalYAML(nil)
if err != nil { return err }
_ = os.WriteFile("/tmp/cur.yaml", b, 0o644)
```

Keys are sorted lexicographically inside every map, so two snapshots whose values match produce **byte-identical** YAML — diff tools work without flake.

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

## Redaction (optional)

`MarshalYAML(redactor)` reserves the parameter for a future field-tag walker; today, redact-before-dump by calling `state.Redact(redactor)` and marshalling the result yourself:

```go
b, _ := yaml.Marshal(state.Redact(mgr.Redactor()))
```

`/config?redact=true` on the sidecar already uses this path — see the [sidecar recipe](sidecar.md).
