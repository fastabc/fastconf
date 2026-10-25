//go:build ignore

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/s3"
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

	// Production supplies an SDK config with refreshing workload credentials.
	client := sdk.NewFromConfig(aws.Config{
		Region: "us-east-1", Credentials: aws.AnonymousCredentials{},
	}, func(o *sdk.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})

	// Keep client construction separate from selecting and decoding the object.
	provider, err := s3.NewWithClient(s3.Config{
		Bucket: "example", Key: "config.yaml",
	}, client, s3.WithMaxBodyBytes(4<<20)) // Use a 4 MiB limit instead of the 10 MiB default.
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
