# Embedded files and cloud sources

`source.NewFS` ships with the root module. Cloud adapters have independent
`go.mod` files: import only the adapters your application needs. Use each
module's declared Go version; Azure/GCP adapters currently require Go 1.26.
The root module supports Go 1.25. SDK clients belong to the application, which
configures authentication, retries and transport policies and closes clients
after managers and consumers stop.

The [example index](../../../examples/README.md) links complete local programs
using real SDKs against in-process endpoints, without cloud credentials.
Each loading example demonstrates typed `fastconf.Load`, field validation and
strict unknown-field handling as well as its adapter-specific behavior.

## One embedded document

```go
//go:embed config/app.yaml
var defaults embed.FS

// Inside an application initialization function:
p := source.NewFS(defaults, "config/app.yaml")
state, err := fastconf.Load[Config](ctx,
    fastconf.WithFS(defaults),
    fastconf.WithProvider(p),
    fastconf.WithUnknownFields(fastconf.UnknownError),
)
if err != nil { return err }
cfg := state.Value()
_ = cfg // Pass the validated configuration to the application.
```

Import `embed`, `fastconf` and `providers/source`. Paths are relative to the FS
root and must satisfy `fs.ValidPath`; absolute paths and `..` are invalid.
The extension selects the codec. Missing files fail; empty files contribute an
empty layer. The revision is a content hash. `WithFS` controls directory discovery;
`source.NewFS` selects one document. Use an empty FS for discovery when only
explicit providers should contribute data, as the cloud examples do.

The default priority is `contracts.PriorityStatic`. Chain `.WithPriority(n)` or
`.WithMaxBodyBytes(n)` to override it. There is no OS watch path or native watcher.
`Manager.Reload` reopens mutable filesystems; embedded files normally stay fixed.
See the [FS example](../../../examples/fs/README.md).

## AWS S3

Use the application's configured `*s3.Client` with `s3.NewWithClient`:

```go
p, err := s3.NewWithClient(s3.Config{
    Bucket: "configs", Key: "prod/app.yaml",
}, s3Client, s3.WithMaxBodyBytes(4 << 20))
if err != nil { return err }
```

The adapter lives in [`providers/s3`](../../../providers/s3/go.mod). Object
selection and decoding are independent of client credentials. The
[complete example](../../../examples/s3/README.md) uses a local S3-compatible
endpoint and loads the document into a validated struct.

## AWS Parameter Store

```go
p, err := parameterstore.New(parameterstore.Config{
    Path: "/myapp/prod", Decrypt: true,
}, ssmClient)
if err != nil { return err }
```

[`providers/parameterstore`](../../../providers/parameterstore/go.mod) borrows a
configured `*ssm.Client`. `GetParametersByPath` recursively reads every page.
`/myapp/prod/server/host` becomes nested `server.host`; only `/` separates path
segments, while dots remain literal. Values stay strings, including StringList.
FastConf's typed hooks convert these strings into numbers, booleans and lists
when loading a struct. See the [two-page example](../../../examples/parameterstore/README.md).

`Decrypt` defaults to false; enabling it needs the corresponding SecureString/KMS
permissions. Paths must be absolute and non-root; a trailing slash is accepted.
An empty result contributes an empty layer. Pagination failures, repeated tokens,
duplicate parameters, invalid paths and leaf/branch conflicts reject the entire
load. The revision hashes sorted names, values and versions, independently of page
order.

## Google Cloud Storage

```go
p, err := gcs.New(gcs.Config{
    Bucket: "my-configs", Key: "prod/app.yaml",
}, storageClient)
if err != nil { return err }
```

[`providers/gcs`](../../../providers/gcs/go.mod) borrows a `*storage.Client`.
Each load checks metadata. An unchanged generation returns a deep copy of the
decoded cache; a new generation pins the download to that generation, keeping
revision and content consistent. Missing objects fail and preserve an existing
manager snapshot. The application closes the client. See the
[GCS example](../../../examples/gcs/README.md).

## Azure Blob Storage

```go
p, err := azblob.New(azblob.Config{
    Container: "configs", Key: "prod/app.yaml",
}, blobClient)
if err != nil { return err }
```

[`providers/azblob`](../../../providers/azblob/go.mod) borrows an `*azblob.Client`
configured for token credentials, shared keys, SAS or an emulator. After a
successful decode, subsequent loads send `If-None-Match`; a 304 returns a deep
copy of the cache. Read/decode failures do not replace the cache. Provider names
omit SAS queries and URL user information; the client remains unchanged. See the
[conditional-request example](../../../examples/azblob/README.md).

## AWS AppConfig

```go
p, err := appconfig.New(appconfig.Config{
    Application: "app-id", Environment: "environment-id", Profile: "profile-id",
    // Codec: "yaml", // Otherwise use the response Content-Type.
}, appConfigDataClient)
if err != nil { return err }
```

[`providers/appconfig`](../../../providers/appconfig/go.mod) borrows an
`*appconfigdata.Client` and uses the Data API. Each token is consumed once. If a
failure leaves token consumption uncertain, the next permitted poll rebuilds the
session. `MinimumPollInterval` defaults to 60 seconds and accepts whole seconds
from 15 seconds to 24 hours; a longer server-directed interval wins.
An early `Load` returns cached content or the previous error without bypassing
rate limits. Empty responses preserve the document. After a decode failure,
empty responses continue returning that error until valid content arrives.
Revisions are content hashes and never expose tokens.

Keep periodic reloads during gradual deployments; SNS deployment notifications
do not replace polling. Applications already running AppConfig Agent can use
the HTTP provider against its local endpoint instead. See the
[session/cache example](../../../examples/appconfig/README.md).

## Azure App Configuration

```go
p, err := azappconfig.New(azappconfig.Config{
    Name: "app-config-prod", Prefix: "app:", Label: "prod",
}, appConfigClient, azappconfig.WithJSONValues())
if err != nil { return err }
```

[`providers/azappconfig`](../../../providers/azappconfig/go.mod) borrows an
`*azappconfig.Client`. Prefix and label matching is literal; filter special
characters are escaped. Empty labels select unlabeled settings only. After prefix
removal, `server:host` becomes a nested path; dots stay literal.
Values remain strings by default. `WithJSONValues()` decodes settings with
`application/json` Content-Type, including `charset` parameters, preserving exact
JSON numbers. Other values remain strings. See the
[typed JSON example](../../../examples/azappconfig/README.md).

Duplicate keys, empty segments, leaf/branch conflicts and pagination failures
reject the whole load. Revisions hash sorted key, label, ETag and value entries.
Deleted settings disappear on the next complete read. This is not a transactional
snapshot across settings. Key Vault references and special App Configuration
content types are rejected; this adapter does not resolve those references or
evaluate Azure feature flags.

## GCP Secret Manager

```go
p, err := secretmanager.New(secretmanager.Config{
    Name: "projects/my-project/secrets/app-config/versions/latest", Codec: "json",
}, secretManagerClient)
if err != nil { return err }
```

[`providers/secretmanager`](../../../providers/secretmanager/go.mod) treats one
secret payload as a complete document. For field-level references use a
[secret resolver](../processing/secrets.md). Names accept fixed versions, `latest`
or service-supported aliases; regional resources need the matching client
endpoint. Each reload calls AccessSecretVersion, verifies CRC32C when present,
and uses the returned version resource name as its revision.

Disabled/destroyed versions fail and preserve the last valid snapshot; they do
not erase plaintext already loaded into the process. The application chooses
whether such errors should stop service. This provider does not automatically
mark fields sensitive: use `fc:"secret"` or `WithSecretPaths` before exposing
Map, Dump or Diff. The [example](../../../examples/secretmanager/README.md)
prints the resolved version and masked typed diagnostics.

## Refresh and size limits

The six non-S3 cloud adapters above default to `contracts.PriorityKV` and a
4 MiB limit, configurable through `Config.Priority` and `WithMaxBodyBytes`.
Non-positive body limits restore the default. FS/GCS/Azure Blob bound document
bytes before decoding; Parameter Store bounds cumulative names and values.
AppConfig, Azure settings and Secret Manager SDKs buffer responses before the
adapter checks decoded input or cumulative fields; these limits do not bound
all transport memory. Oversized inputs return `contracts.ErrTooLarge`.
GCS/Azure Blob select codecs by key extension; set `Config.Codec` for keys without
an extension. S3 has its own defaults; the example explicitly sets 4 MiB.

These loaders start no polling or native watchers. Pass them to
`fastconf.WithProvider` in `Load` or `New`. Call `Manager.Reload` with a bounded
context, or use `WithNotifier` and `WithResync` for managed notifications and
periodic recovery. See [cloud notifications](cloud-notifications.md) and
[reload failure handling](../runtime/reload-policy.md).

Validate against this checkout from the repository root:

```sh
GOPROXY=direct make example
GOPROXY=direct make test-workspace MODULES=". providers/parameterstore providers/gcs providers/azblob"
```
