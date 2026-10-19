# Strategic merge (`mergeKeys`)

Lists replace by default. Root metadata `spec.appendSlices: true` enables
append behavior; `mergeKeys` instead merges list objects by an identity field.
This is a keyed-list operation, not the full Kubernetes strategic-merge protocol.

`mergeKeys` maps dotted list paths to identity field names. Identity values must
be non-empty strings: matching objects merge recursively and new keys append.
Source non-map entries and objects without a valid key append unchanged.
Use unique keys in each input list; duplicate keys are not rejected.
There is no `$patch` delete/replace directive.

## Via `_meta.yaml`

```yaml
# conf.d/_meta.yaml
spec:
  mergeKeys:
    containers: name           # entries identified by .name
    "spec.services": id        # entries identified by .id
```

## Via Option

```go
mgr, err := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithProfile(fastconf.Profile{Names: []string{"prod"}}),
    fastconf.WithMergeKeys(map[string]string{
        "containers": "name",
    }),
)
if err != nil {
    return err
}
defer mgr.Close()
```

Programmatic entries win over `_meta.yaml` entries on conflict.

## Example

`conf.d/base/00.yaml`:

```yaml
containers:
  - {name: api,     image: img:v1, port: 8080}
  - {name: sidecar, image: side:v1}
```

`conf.d/overlays/prod/50.yaml`:

```yaml
containers:
  - {name: api,     image: img:v2}     # only override the api image
  - {name: cron,    image: cron:v1}    # new entry, appended
```

With prod selected and `mergeKeys: {containers: name}`, the result is:

```yaml
containers:
  - {name: api,     image: img:v2, port: 8080}   # port preserved
  - {name: sidecar, image: side:v1}              # untouched
  - {name: cron,    image: cron:v1}              # new entry
```

## Cost

Key matching builds an index and walks both lists; nested object merging adds
the cost of their fields. Only registered paths use keyed merging; other lists
retain replace/append behavior. Nested keyed-list paths use the parent `[]`
segment, for example `containers[].ports`.
