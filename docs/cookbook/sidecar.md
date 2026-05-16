# `fastconfd` sidecar

`cmd/fastconfd` is a tiny HTTP+SSE daemon that embeds a `fastconf.Manager[map[string]any]`. Run it next to a polyglot workload that does not want to link the Go SDK.

## Run

```bash
go run github.com/fastabc/fastconf/cmd/fastconfd \
  -dir /etc/myapp/conf.d \
  -addr :8650 \
  -reload-token "$(cat /var/run/secrets/reload-token)"
```

## Endpoints

| Method | Path                          | Notes                              |
|--------|-------------------------------|------------------------------------|
| GET    | `/healthz`                    | 200 once first reload OK           |
| GET    | `/version`                    | `{generation, hash, loaded_at, reason}` |
| GET    | `/config`                     | Full snapshot (JSON)               |
| GET    | `/config?path=db.host`        | Dotted-path lookup                 |
| GET    | `/config?redact=true`         | Snapshot with `fastconf:"secret"` fields masked |
| GET    | `/config?path=…&redact=true`  | Redacted dotted-path lookup |
| GET    | `/dump`                       | Deterministic YAML rendering of merged state |
| GET    | `/dump?format=json`           | Same content as JSON |
| POST   | `/reload`                     | `X-Reload-Token: <secret>` required |
| GET    | `/events`                     | Server-Sent Events of `ReloadCause` |

## Examples

```bash
# Watch reloads land in real time
curl -N http://localhost:8650/events

# Diff the live state against your repo
curl -s http://localhost:8650/dump > /tmp/live.yaml
diff -u conf.d/base/00.yaml /tmp/live.yaml

# Fetch a redacted config snapshot for an incident report
curl -s 'http://localhost:8650/config?redact=true' | jq .
```

## Python client

See [`example_sidecar_test.go`](../../example_sidecar_test.go) for a runnable sidecar-style example.
