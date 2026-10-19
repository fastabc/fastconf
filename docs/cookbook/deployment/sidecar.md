# `fastconfd` sidecar

`cmd/fastconfd` is a tiny HTTP+SSE daemon that embeds a `fastconf.Manager[map[string]any]`. Run it next to a polyglot workload that does not want to link the Go SDK.

## Run

```bash
go run github.com/fastabc/fastconf/cmd/fastconfd@v1.1.0 \
  -dir /etc/myapp/conf.d \
  -addr 127.0.0.1:8650 \
  -read-token "$(cat /var/run/secrets/read-token)" \
  -reload-token "$(cat /var/run/secrets/reload-token)"
```

`-reload-token` also defaults from `FASTCONFD_RELOAD_TOKEN`, which is easier to
wire from a Kubernetes Secret.
`-read-token` defaults from `FASTCONFD_READ_TOKEN` and is required for
`/config`, `/dump`, and `/events` via `X-Config-Token`. Both tokens are
required at startup. The default bind address is loopback.

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
| GET    | `/version`                    | `{version, generation, hash, loaded_at, reason}`; `loaded_at` is an RFC 3339 timestamp string |
| GET    | `/config`                     | Full snapshot (JSON), secrets masked, `X-Config-Token` required |
| GET    | `/config?path=db.host`        | Dotted-path lookup (masked)        |
| GET    | `/config?unredacted=true`     | Plaintext; also needs `X-Unredacted-Token` (`-unredacted-token`) |
| GET    | `/dump`                       | Deterministic YAML rendering, secrets masked, `X-Config-Token` required |
| GET    | `/dump?format=json`           | Same content as JSON |
| POST   | `/reload`                     | `X-Reload-Token: <secret>` required |
| GET    | `/events`                     | Server-Sent Events, `X-Config-Token` required |

## Kubernetes sidecar

The image names below are placeholders for images built and published by your
organization; the release workflow distributes binaries, not container images.
The `myapp-config` ConfigMap must provide an `app.yaml` key containing the
application YAML; the volume maps it into the scanned base directory.

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
            - name: FASTCONFD_READ_TOKEN
              valueFrom:
                secretKeyRef:
                  name: fastconfd-reload
                  key: read-token
        - name: fastconfd
          image: example/fastconfd:v1.1.0
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
          volumeMounts:
            - name: config
              mountPath: /etc/myapp/conf.d
              readOnly: true
      volumes:
        - name: config
          configMap:
            name: myapp-config
            items:
              - key: app.yaml
                path: base/00-app.yaml
```

The application image must use `FASTCONFD_ADDR` and send `FASTCONFD_READ_TOKEN`
in the `X-Config-Token` header; these environment variables do not configure an
HTTP client automatically.

The example omits probes because the listener binds to loopback. Add an
image-specific exec probe for `http://127.0.0.1:8650/healthz` if the image has
an HTTP client. A Pod-IP HTTP probe requires an address reachable through the
Pod network. See the Kubernetes
[probe configuration reference](https://kubernetes.io/docs/concepts/configuration/liveness-readiness-startup-probes/#http-probes).

Do not create a Service or Ingress for `fastconfd` unless another workload
genuinely needs cross-pod access. If `/reload` is exposed outside the pod,
require TLS and rotate the token through the Secret.

## Client behavior

SSE events carry a reload cause, not the configuration or a durable cursor.
Fetch `/config` at startup and after notifications/reconnects; account for
changes between fetching and subscribing. The event bus is bounded and may
drop notifications, so periodic generation checks/refetches are appropriate
when eventual synchronization is required. `/healthz` indicates an available
snapshot; it does not mean the latest reload succeeded.

## Examples

```bash
# Watch reloads land in real time
curl -N -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/events

# Trigger a manual reload
curl -X POST -H "X-Reload-Token: $FASTCONFD_RELOAD_TOKEN" \
  http://localhost:8650/reload

# Diff the live state against your repo
curl -s -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/dump > /tmp/live.yaml
fastconfctl dump -dir conf.d -profile prod -format yaml > /tmp/expected.yaml
diff -u /tmp/expected.yaml /tmp/live.yaml

# Fetch a redacted config snapshot for an incident report
curl -s -H "X-Config-Token: $FASTCONFD_READ_TOKEN" http://localhost:8650/config | jq .
```

## HTTP integration tests

[`cmd/fastconfd/main_test.go`](../../../cmd/fastconfd/main_test.go) exercises
the daemon's authentication, redacted views and SSE commit delivery.
Run it with `go test ./cmd/fastconfd -run '^TestServer_'`.
