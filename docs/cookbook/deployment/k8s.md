# Kubernetes recipe

Two patterns, pick one.

## A. In-process (preferred for Go services)

Mount the ConfigMap key `app.yaml` at `base/00-app.yaml`; FastConf scans the
base directory under `WithDir`. Map overlay files under `overlays/<profile>/`
in the same way. The snippets below are container/pod spec fragments.

```yaml
volumeMounts:
  - name: cfg
    mountPath: /etc/myapp/conf.d
    readOnly: true
volumes:
  - name: cfg
    configMap:
      name: myapp-config
      items:
        - key: app.yaml
          path: base/00-app.yaml
```

```go
mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithDir("/etc/myapp/conf.d"),
    fastconf.WithProfile(fastconf.Profile{Env: "APP_PROFILE"}),
    fastconf.WithWatch(fastconf.Watch{}),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Mounted ConfigMaps and Secrets update asynchronously. Projection can take
the kubelet sync period plus cache propagation delay; a projected Secret
does not provide instant rotation. See the Kubernetes documentation on
[mounted Secret updates](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod).

### Downward API metadata

If the app also needs mounted pod metadata, register the Downward API
provider alongside the file watcher:

```yaml
volumeMounts:
  - name: podinfo
    mountPath: /etc/podinfo
    readOnly: true
volumes:
  - name: podinfo
    downwardAPI:
      items:
        - path: "labels"
          fieldRef: { fieldPath: metadata.labels }
        - path: "annotations"
          fieldRef: { fieldPath: metadata.annotations }
```

```go
import k8s "github.com/fastabc/fastconf/providers/k8s"

mgr, err := fastconf.New[MyApp](ctx,
    fastconf.WithDir("/etc/myapp/conf.d"),
    fastconf.WithProvider(k8s.NewDefault()),
    fastconf.WithWatch(fastconf.Watch{}),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Downward API volume refreshes use the same projected-volume `..data`
atomic-swap pattern as ConfigMaps. FastConf automatically adds the provider's
mounted files to the shared watcher, so metadata changes can enter the normal
reload loop when `WithWatch(Watch{})` is enabled. Mount the volume normally;
do **not** use `subPath`, which bypasses projected-volume refreshes.

`k8s.NewDefault()` preserves raw metadata keys under `k8s.metadata.*` by
default, e.g. `k8s.metadata.labels["app.kubernetes.io/name"]`. Only opt into
`MetadataExpanded` when your application intentionally treats metadata keys as
configuration paths.

## B. Sidecar (preferred for non-Go services)

Run `fastconfd` next to your app container; the app polls
`http://localhost:8650/config` at startup and follows
`/events` as a signal to fetch a fresh snapshot. Both endpoints require
`X-Config-Token`. SSE notifications are not a durable replay log; clients need
a reconnect/refetch strategy. See [sidecar.md](sidecar.md).

## Runnable example

[`ExampleLoad_profiles`](../../../example_api_test.go) demonstrates profile-aware
overlay loading with `fastconf.WithProfile`. Run it with
`go test . -run '^ExampleLoad_profiles$' -v`.
