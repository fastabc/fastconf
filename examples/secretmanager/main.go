//go:build ignore

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"time"

	sdk "cloud.google.com/go/secretmanager/apiv1"
	"github.com/fastabc/fastconf/providers/secretmanager"
	"google.golang.org/api/option"
)

func main() {
	// Serve a versioned document and its CRC32C checksum without cloud credentials.
	body := []byte(`{"host":"localhost"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "projects/local/secrets/config/versions/1",
			"payload": map[string]any{
				"data":       base64.StdEncoding.EncodeToString(body),
				"dataCrc32c": int64(crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))),
			},
		})
	}))
	defer server.Close()

	// The application owns the authenticated SDK client in production.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := sdk.NewRESTClient(ctx,
		option.WithEndpoint(server.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(server.Client()),
	)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Resolve latest on each load and decode the payload as JSON.
	p, err := secretmanager.New(secretmanager.Config{
		Name: "projects/local/secrets/config/versions/latest", Codec: "json",
	}, client)
	if err != nil {
		panic(err)
	}

	// Load verifies the checksum before returning the document.
	snapshot, err := p.Load(ctx)
	if err != nil {
		panic(err)
	}

	// Print the version, never the secret document. Configure FastConf secret
	// paths/tags before exposing snapshots or diffs in a real application.
	fmt.Println("loaded version:", snapshot.Revision)
}
