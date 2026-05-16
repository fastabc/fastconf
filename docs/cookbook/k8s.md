# Kubernetes recipe

Two patterns, pick one.

## A. In-process (preferred for Go services)

Mount your ConfigMap as a volume; FastConf watches the directory.

```yaml
volumeMounts:
  - name: cfg
    mountPath: /etc/myapp/conf.d
    readOnly: true
volumes:
  - name: cfg
    configMap:
      name: myapp-config
```

```go
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithDir("/etc/myapp/conf.d"),
    fastconf.WithProfileEnv("APP_PROFILE"),
    fastconf.WithWatch(true),
)
```

ConfigMap edits propagate to the volume after the kubelet's sync
period (~60s). For instant rotation, use a `Secret` mounted with
`projected` volume + `defaultMode`.

## B. Sidecar (preferred for non-Go services)

Run `fastconfd` next to your app container; the app polls
`http://localhost:8650/config` once per startup and follows
`/events` for live updates. See [sidecar.md](sidecar.md).
