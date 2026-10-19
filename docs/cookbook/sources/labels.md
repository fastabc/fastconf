# Labels: configuration, routing and metadata

Choose the entry point by the meaning of the labels. Kubernetes metadata keys
should remain literal unless the application explicitly treats them as paths.

| Input | API | Default behavior |
|---|---|---|
| Dotted configuration labels | `labels.New(input, labels.Options{})` | Expand paths; retain string values |
| Routing DSL labels | `labels.New(input, labels.Options{Routing: &labels.Routing{}})` | Parse scalars, lists and indexed paths |
| A label field already in configuration | `WithTransform` and `confmap.ExpandLabels` | Expand the existing field into the merged tree |
| Downward API metadata | `k8s.NewDefault()` | Preserve raw keys under `k8s.metadata` |

Code blocks are integration fragments. Supply `Cfg` and `ctx`; `return err`
assumes a function returning error. Import `fastconf`, `confmap`, `contracts`
and providers as used below.

## Dotted configuration labels

`labels.New` accepts `[]string` / `[]any` containing `key=value` strings, or
`map[string]string` / `map[string]any`:

```go
import labelprovider "github.com/fastabc/fastconf/providers/labels"

input := []string{"server.addr=:9090", "feature.rollout=canary"}
mgr, err := fastconf.New[Cfg](ctx,
    fastconf.WithProvider(labelprovider.New(input, labelprovider.Options{})),
)
if err != nil {
    return err
}
defer mgr.Close()
```

For prefixed input, filter and remove the prefix before expansion:

```go
p := labelprovider.New(map[string]string{"config.server.addr": ":9090"},
    labelprovider.Options{Prefix: "config.", StripPrefix: true})
```

| Option | Meaning | Default |
|---|---|---|
| `Name` / `Priority` | Diagnostic name and provider ordering | `"labels:<Prefix>"` / `PriorityStatic` |
| `Prefix` / `StripPrefix` | Filter keys; optionally remove the prefix | `""` / `false` |
| `Separators` | Path separators | `[]string{"."}` |
| `Coerce` | Convert string bool/int/float values eagerly | `false` |
| `Routing` | Enable the routing DSL | `nil` |

All providers follow file and generator layers. Set `Options.Priority` to order
labels against other providers; equal priorities retain registration order.
Typed struct hooks can convert string scalars later without eager coercion.

## Routing DSL

A non-nil `Options.Routing` adds scalar conversion, comma-separated lists and
indexed paths such as `domains[0].main`. An optional enable gate can skip the
whole label set:

```go
input := []string{
    "routing.enable=true",
    "routing.http.services.api.loadbalancer.server.port=8080",
    "routing.http.routers.api.entrypoints=web,websecure",
    "routing.http.routers.api.tls.domains[0].main=example.com",
    "routing.http.routers.api.tls.domains[0].sans=www.example.com,api.example.com",
}
p := labelprovider.New(input, labelprovider.Options{
    Routing: &labelprovider.Routing{EnableGate: "routing.enable"},
})
```

| Routing option | Meaning | Default |
|---|---|---|
| `EnableGate` | When present with a non-truthy value, skip the entire set | `""` (disabled) |
| `ListSeparator` / `NoListSplit` | List delimiter / disable list splitting | `","` / `false` |
| `KeepRawSuffixes` | Protect expression fields from scalar/list rewriting | `[".rule", "regexp"]` |
| `Raw` | Retain all values without scalar/list conversion | `false` |
| `LowercaseKeys` | Lowercase keys before expansion | `false` |

`NoListSplit` still permits scalar coercion; `Raw` disables both. Add raw
suffixes or disable splitting when expression values contain commas.

## Labels inside a configuration document

Given `conf.d/base/00-app.yaml`:

```yaml
deploy:
  labels:
    - "server.port=9999"
    - "routing.enable=true"
```

Expand the field during the transform stage:

```go
expandLabels := func(root map[string]any) error {
    raw, ok := confmap.GetDotted(root, "deploy.labels")
    if !ok {
        return nil
    }
    confmap.DeleteDotted(root, "deploy.labels")
    // Merge expanded label values over the existing configuration.
    return confmap.Deep(root,
        confmap.ExpandLabels(raw, confmap.LabelOptions{}), confmap.Options{})
}
```

Register with `fastconf.WithTransform(expandLabels)`. The result contains
`server.port: "9999"` and `routing.enable: "true"`; values remain strings
until typed decoding unless coercion is enabled.

`confmap.ExpandLabels` is also usable without a manager:

```go
tree := confmap.ExpandLabels(
    []string{"a.b.c=1", "a.b.d=2"}, confmap.LabelOptions{},
)
// tree contains a.b.c="1" and a.b.d="2".
```

## Kubernetes metadata

```go
import k8s "github.com/fastabc/fastconf/providers/k8s"

mgr, err := fastconf.New[Cfg](ctx,
    fastconf.WithProvider(k8s.NewDefault()),
    fastconf.WithWatch(fastconf.Watch{}),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Default output preserves keys such as
`k8s.metadata.labels["app.kubernetes.io/name"]`. The shared file watcher follows
mounted metadata changes when `WithWatch` is enabled. See the
[Kubernetes recipe](../deployment/k8s.md) for volume layout.

Opt into path expansion only when the application requires it:

```go
fastconf.WithProvider(k8s.New(k8s.Options{
    LabelsPath:      "/etc/podinfo/labels",
    AnnotationsPath: "/etc/podinfo/annotations",
    At:              "k8s.metadata",
    LabelsMode:      k8s.MetadataExpanded,
    AnnotationsMode: k8s.MetadataExpanded,
    Separators:      []string{"/", "."},
}))
```

`k8s.NewExpandedDefault()` explicitly selects the expanded-root shape. Avoid
expansion when consumers need the original selector/annotation key spelling.

## Input limits

Strings split at the first `=`; missing delimiters and empty keys are skipped.
The input is a string list or map, not a list of `{key, value}` objects.
Expansion reshapes keys; choose the raw metadata mode when that would change
application meaning. See [configuration ownership](../../design/spec.md#configuration-and-ownership)
for provider setup and snapshot immutability.
