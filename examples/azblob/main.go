//go:build ignore

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	sdk "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/fastabc/fastconf/providers/azblob"
)

func main() {
	const etag = `"example-v1"`
	// A local Azure-compatible endpoint returns 304 on the second load.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("ETag", etag)
		fmt.Fprintln(w, "server:\n  port: 9090")
	}))
	defer server.Close()

	// For Azure, use your application's credential/shared-key/SAS client instead.
	client, err := sdk.NewClientWithNoCredential(server.URL, nil)
	if err != nil {
		panic(err)
	}
	provider, err := azblob.New(azblob.Config{Container: "example", Key: "config.yaml"}, client)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Applications can pass provider directly to fastconf.WithProvider.
	first, err := provider.Load(ctx)
	if err != nil {
		panic(err)
	}
	second, err := provider.Load(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(second.Map)
	fmt.Printf("revision=%s unchanged=%t\n", second.Revision, first.Revision == second.Revision)
}
