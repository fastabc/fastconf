//go:build ignore

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/fastabc/fastconf/providers/appconfig"
)

func main() {
	// Serve session creation and the first configuration without an AWS account.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/configurationsessions" {
			fmt.Fprintln(w, `{"InitialConfigurationToken":"local-token"}`)
			return
		}
		w.Header().Set("Next-Poll-Configuration-Token", "next-token")
		w.Header().Set("Next-Poll-Interval-In-Seconds", "60")
		fmt.Fprintln(w, `{"server":{"host":"localhost"}}`)
	}))
	defer server.Close()

	// In AWS, use your application's configured credentials/HTTP client instead.
	client := sdk.NewFromConfig(aws.Config{
		Region: "us-east-1", Credentials: aws.AnonymousCredentials{},
	}, func(o *sdk.Options) { o.BaseEndpoint = aws.String(server.URL) })

	// Select one deployed application/environment/profile document.
	p, err := appconfig.New(appconfig.Config{Application: "app", Environment: "prod", Profile: "config"}, client)
	if err != nil {
		panic(err)
	}

	// Bound both loads; the second must respect the server-directed poll interval.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first, err := p.Load(ctx)
	if err != nil {
		panic(err)
	}

	// An immediate reload returns a copy of the cached configuration.
	second, err := p.Load(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(second.Map)
	fmt.Println("cached before next poll:", first.Revision == second.Revision)
}
