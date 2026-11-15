//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing/fstest"
	"time"

	"cloud.google.com/go/storage"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/gcs"
	"google.golang.org/api/option"
)

type Config struct {
	Server struct {
		Port int `json:"port" fc:"min=1,max=65535"`
	} `json:"server"`
}

func main() {
	const body = "server:\n  port: 9090\n"

	// Serve metadata and YAML locally, using the real GCS SDK's JSON transport.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") == "media" {
			w.Header().Set("Content-Type", "application/yaml")
			w.Header().Set("X-Goog-Generation", "1")
			fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"bucket": "example", "name": "config.yaml", "generation": "1", "size": strconv.Itoa(len(body)),
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// For GCP, use your application's authenticated storage.Client instead.
	client, err := storage.NewClient(ctx, option.WithEndpoint(server.URL+"/storage/v1/"),
		option.WithoutAuthentication(), option.WithHTTPClient(server.Client()),
		storage.WithJSONReads(), storage.WithDisabledClientMetrics())
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Select one object; Load pins its download to the observed generation.
	provider, err := gcs.New(gcs.Config{Bucket: "example", Key: "config.yaml"}, client)
	if err != nil {
		panic(err)
	}

	// Applications can pass provider directly to fastconf.WithProvider.
	first, err := provider.Load(ctx)
	if err != nil {
		panic(err)
	}

	// The same generation reuses the decoded document after a metadata check.
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
