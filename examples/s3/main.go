//go:build ignore

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/fastabc/fastconf/providers/s3"
)

func main() {
	// A local S3-compatible endpoint makes this example runnable without AWS.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("ETag", `"example-v1"`)
		fmt.Fprintln(w, "server:\n  port: 9090")
	}))
	defer server.Close()

	provider, err := s3.New(s3.Config{
		Bucket: "example", Key: "config.yaml", Region: "us-east-1",
		AccessKey: "example-access", SecretKey: "example-secret",
		Endpoint: server.URL, PathStyle: true,
	})
	if err != nil {
		panic(err)
	}
	// Applications can pass provider directly to fastconf.WithProvider.
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println(snapshot.Map)
}
