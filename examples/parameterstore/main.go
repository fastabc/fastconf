//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fastabc/fastconf/providers/parameterstore"
)

func main() {
	// A local SSM endpoint serves two pages without an AWS account or credentials.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ NextToken string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if request.NextToken == "page2" {
			fmt.Fprintln(w, `{"Parameters":[{"Name":"/example/server/port","Value":"9090","Version":1}]}`)
			return
		}
		fmt.Fprintln(w, `{"Parameters":[{"Name":"/example/server/host","Value":"localhost","Version":1}],"NextToken":"page2"}`)
	}))
	defer server.Close()

	// For AWS, supply your application's configured aws.Config instead.
	client := ssm.NewFromConfig(aws.Config{
		Region: "us-east-1", Credentials: aws.AnonymousCredentials{},
	}, func(o *ssm.Options) { o.BaseEndpoint = aws.String(server.URL) })
	provider, err := parameterstore.New(parameterstore.Config{Path: "/example", Decrypt: true}, client)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Applications can pass provider directly to fastconf.WithProvider.
	snapshot, err := provider.Load(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(snapshot.Map)
}
