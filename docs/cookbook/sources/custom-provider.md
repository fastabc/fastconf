# Custom providers

`contracts.ReadAllMax` and `contracts.SafeError` are added in v1.3.0.
This complete program loads a small document from a custom source:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "github.com/fastabc/fastconf"
    "github.com/fastabc/fastconf/contracts"
)

type document struct{}

func (document) Name() string { return "custom" }
func (document) Describe() contracts.ProviderInfo {
    return contracts.ProviderInfo{Priority: contracts.PriorityKV}
}
func (document) Load(ctx context.Context) (contracts.Snapshot, error) {
    if err := ctx.Err(); err != nil {
        return contracts.Snapshot{}, err
    }
    body, err := contracts.ReadAllMax(strings.NewReader(`{"port":8080}`), 0)
    if err != nil {
        return contracts.Snapshot{}, contracts.SafeError("custom: read failed", err)
    }
    var tree map[string]any
    if err := json.Unmarshal(body, &tree); err != nil {
        return contracts.Snapshot{}, contracts.SafeError("custom: decode failed", err)
    }
    return contracts.Snapshot{Map: tree}, nil
}

func main() {
    state, err := fastconf.Load[map[string]any](context.Background(),
        fastconf.WithProvider(document{}))
    if err != nil {
        panic(err)
    }
    fmt.Println(state.Value())
}
```

`Load` still discovers `conf.d` by default; any local files participate in the
normal layer order. Run in a directory without `conf.d` to use only this source.

Save this companion test as `main_test.go` beside the program:

```go
package main

import (
    "testing"

    "github.com/fastabc/fastconf/contracts/providertest"
)

func TestDocument(t *testing.T) {
    providertest.AssertProviderBasics(t, document{})
}
```

For remote I/O, bind requests to `ctx`, close response bodies, and apply bounded
reads before decoding. A non-positive limit uses `DefaultMaxBodyBytes` (4 MiB);
errors wrap `ErrTooLarge` rather than returning truncated input. `SafeError`
prints only your message while retaining the cause for `errors.Is`/`errors.As`.
Keep credentials and payloads out of that message; printing an unwrapped SDK
error can expose them again.

The provider owns its returned map; the manager clones it before merging.
Configure providers before registration and implement `Watcher` only for native
notifications. See [ownership](../../design/spec.md#configuration-and-ownership)
and [timeouts](../runtime/provider-timeouts.md).
