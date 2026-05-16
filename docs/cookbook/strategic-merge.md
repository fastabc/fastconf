# Strategic merge (`mergeKeys`)

By default FastConf treats slices as either **append** (`appendSlices: true`) or **replace** (the default). Neither is right for "list of objects identified by a key field" — the Kubernetes-style case where overlays should patch *one* container's image without touching the others.

`mergeKeys` declares which slice paths are keyed and what field names identify each entry. Aligned entries merge in place; new keys append; non-map entries pass through unchanged.

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
mgr, _ := fastconf.New[Cfg](ctx,
    fastconf.WithDir("conf.d"),
    fastconf.WithMergeKeys(map[string]string{
        "containers": "name",
    }),
)
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

Result with `mergeKeys: {containers: name}`:

```yaml
containers:
  - {name: api,     image: img:v2, port: 8080}   # port preserved
  - {name: sidecar, image: side:v1}              # untouched
  - {name: cron,    image: cron:v1}              # new entry
```

## Cost

Strategic merge is O(len(dst)+len(src)) — one pass to build an index, one pass to align/append. It is **only** active for paths listed in `mergeKeys`; every other slice retains the existing replace/append semantics.
