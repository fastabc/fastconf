//go:build ignore

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	sdk "github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/fastabc/fastconf/providers/azappconfig"
)

func main() {
	// Serve one labeled setting using the real SDK against a local endpoint.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.microsoft.appconfig.kvset+json")
		w.Header().Set("Sync-Token", "local=token;sn=1")
		fmt.Fprintln(w, `{"items":[{"key":"app:server:host","label":"prod","value":"localhost","etag":"one"}]}`)
	}))
	defer server.Close()

	// This key signs only local requests. Production applications supply their
	// own SDK client, usually constructed with a token credential.
	client, err := sdk.NewClientFromConnectionString("Endpoint="+server.URL+";Id=local;Secret=bG9jYWw=", nil)
	if err != nil {
		panic(err)
	}

	// Remove the prefix and expand colon-separated keys into a nested tree.
	p, err := azappconfig.New(azappconfig.Config{Prefix: "app:", Label: "prod"}, client)
	if err != nil {
		panic(err)
	}

	// Load all selected settings within one deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	snapshot, err := p.Load(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(snapshot.Map)
}
