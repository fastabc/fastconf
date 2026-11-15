//go:build ignore

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing/fstest"
	"time"

	sdk "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/azblob"
)

type Config struct {
	Server struct {
		Port int `json:"port" fc:"min=1,max=65535"`
	} `json:"server"`
}

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

	// Select the blob independently of the client authentication mechanism.
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

	// A matching ETag reuses the decoded document after the server returns 304.
	second, err := provider.Load(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(second.Map)
	fmt.Printf("revision=%s unchanged=%t\n", second.Revision, first.Revision == second.Revision)

	// Use an empty discovery FS so only the explicit provider contributes data.
	state, err := fastconf.Load[Config](ctx, fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(provider), fastconf.WithUnknownFields(fastconf.UnknownError))
	if err != nil {
		panic(err)
	}
	fmt.Printf("typed port=%d\n", state.Value().Server.Port)
}
