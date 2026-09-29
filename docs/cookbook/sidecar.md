# `fastconfd` sidecar

`cmd/fastconfd` is a tiny HTTP+SSE daemon that embeds a `fastconf.Manager[map[string]any]`. Run it next to a polyglot workload that does not want to link the Go SDK.

## Run

```bash
go run github.com/fastabc/fastconf/cmd/fastconfd \
  -dir /etc/myapp/conf.d \
  -addr 127.0.0.1:8650 \
  -read-token "$(cat /var/run/secrets/read-token)" \
  -reload-token "$(cat /var/run/secrets/reload-token)"
```

`-reload-token` also defaults from `FASTCONFD_RELOAD_TOKEN`, which is easier to
wire from a Kubernetes Secret.
`-read-token` defaults from `FASTCONFD_READ_TOKEN` and is required for
`/config`, `/dump`, and `/events` via `X-Config-Token`. Both tokens are
required at startup; the explicit loopback address is the safe default.

The sidecar serves `map[string]any`, which has no `fc:"secret"` tags, so it
redacts by path pattern instead. Keys named `password`, `passwd`, `secret`,
`token`, `api_key`, `apikey`, and `private_key` are masked at any depth; add
more with the repeatable `-secret-path` flag (`*` = one segment, `**` = any).
Plaintext output is off unless `-unredacted-token` (or
`FASTCONFD_UNREDACTED_TOKEN`) is set; requests then pass `?unredacted=true`
with `X-Unredacted-Token`:

```bash
fastconfd ... -secret-path 'db.dsn' -secret-path '**.credentials'
```

## Endpoints

| Method | Path                          | Notes                              |
|--------|-------------------------------|------------------------------------|
| GET    | `/healthz`                    | 200 once first reload OK           |
| GET    | `/version`                    | `{generation, hash, loaded_at, reason}` |
| GET    | `/config`                     | Full snapshot (JSON), secrets masked, `X-Config-Token` required |
| GET    | `/config?path=db.host`        | Dotted-path lookup (masked)        |
| GET    | `/config?unredacted=true`     | Plaintext; also needs `X-Unredacted-Token` (`-unredacted-token`) |
| GET    | `/dump`                       | Deterministic YAML rendering, secrets masked, `X-Config-Token` required |
| GET    | `/dump?format=json`           | Same content as JSON |
| POST   | `/reload`                     | `X-Reload-Token: <secret>` required |
| GET    | `/events`                     | Server-Sent Events, `X-Config-Token` required |

## Kubernetes sidecar

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: fastconfd-reload
type: Opaque
stringData:
  token: change-me
  read-token: change-me-read
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  replicas: 2
  selector:
    matchLabels:
      app: myapp
  template:
    metadata:
      labels:
        app: myapp
    spec:
      containers:
        - name: app
          image: example/myapp:latest
          env:
            - name: FASTCONFD_ADDR
              value: http://127.0.0.1:8650
        - name: fastconfd
          image: example/fastconfd:vX.Y.Z
          args:
            - -dir=/etc/myapp/conf.d
            - -addr=127.0.0.1:8650
          env:
            - name: FASTCONFD_RELOAD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: fastconfd-reload
                  key: token
            - name: FASTCONFD_READ_TOKEN
              valueFrom:
                secretKeyRef:
                  name: fastconfd-reload
                  key: read-token
          ports:
            - name: http
              containerPort: 8650
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
          volumeMounts:
            - name: config
              mountPath: /etc/myapp/conf.d
              readOnly: true
      volumes:
        - name: config
          configMap:
            name: myapp-config
```

Do not create a Service or Ingress for `fastconfd` unless another workload
genuinely needs cross-pod access. If `/reload` is exposed outside the pod,
require TLS and rotate the token through the Secret.

## Examples

```bash
# Watch reloads land in real time
curl -N -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/events

# Trigger a manual reload
curl -X POST -H "X-Reload-Token: $FASTCONFD_RELOAD_TOKEN" \
  http://localhost:8650/reload

# Diff the live state against your repo
curl -s -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/dump > /tmp/live.yaml
diff -u conf.d/base/00.yaml /tmp/live.yaml

# Fetch a redacted config snapshot for an incident report
curl -s -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/config | jq .
```

## HTTP integration tests

[`cmd/fastconfd/main_test.go`](../../cmd/fastconfd/main_test.go) exercises
the daemon's authentication, redacted views and SSE commit delivery.
Run it with `go test ./cmd/fastconfd -run '^TestServer_'`.
