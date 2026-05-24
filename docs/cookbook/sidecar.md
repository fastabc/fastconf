# `fastconfd` sidecar

`cmd/fastconfd` is a tiny HTTP+SSE daemon that embeds a `fastconf.Manager[map[string]any]`. Run it next to a polyglot workload that does not want to link the Go SDK.

## Run

```bash
go run github.com/fastabc/fastconf/cmd/fastconfd \
  -dir /etc/myapp/conf.d \
  -addr :8650 \
  -reload-token "$(cat /var/run/secrets/reload-token)"
```

`-reload-token` also defaults from `FASTCONFD_RELOAD_TOKEN`, which is easier to
wire from a Kubernetes Secret.

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

## Kubernetes sidecar

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: fastconfd-reload
type: Opaque
stringData:
  token: change-me
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
            - -addr=:8650
          env:
            - name: FASTCONFD_RELOAD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: fastconfd-reload
                  key: token
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
curl -N http://localhost:8650/events

# Trigger a manual reload
curl -X POST -H "X-Reload-Token: $FASTCONFD_RELOAD_TOKEN" \
  http://localhost:8650/reload

# Diff the live state against your repo
curl -s http://localhost:8650/dump > /tmp/live.yaml
diff -u conf.d/base/00.yaml /tmp/live.yaml

# Fetch a redacted config snapshot for an incident report
curl -s 'http://localhost:8650/config?redact=true' | jq .
```

## Runnable example

[`examples/sidecar/example_test.go`](../../examples/sidecar/example_test.go) —
the in-process sidecar manager (PresetSidecar) that `cmd/fastconfd`
wraps in an HTTP+SSE server. Run it with `go test ./examples/sidecar/...`.
