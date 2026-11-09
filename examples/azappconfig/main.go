//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing/fstest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	sdk "github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/azappconfig"
)

// The real SDK uses an in-memory transport: no listener, cloud account or real key.
type fixtureTransport struct{}

func (fixtureTransport) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Query().Get("key") != "app:*" || r.URL.Query().Get("label") != "prod" {
		return nil, errors.New("unexpected setting selection")
	}
	return &http.Response{StatusCode: http.StatusOK, Request: r,
		Header: http.Header{"Content-Type": {"application/vnd.microsoft.appconfig.kvset+json"}, "Sync-Token": {"local=token;sn=1"}},
		Body:   io.NopCloser(strings.NewReader(`{"items":[{"key":"app:server:host","label":"prod","value":"localhost","etag":"one"},{"key":"app:limits","label":"prod","value":"{\"rps\":10,\"enabled\":true}","content_type":"application/json; charset=utf-8","etag":"two"}]}`))}, nil
}

func run(out io.Writer) error {
	client, err := sdk.NewClientFromConnectionString("Endpoint=https://example.azconfig.io;Id=local;Secret=bG9jYWw=", &sdk.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: fixtureTransport{}},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	selection := azappconfig.Config{Prefix: "app:", Label: "prod"}
	plain, err := azappconfig.New(selection, client)
	if err != nil {
		return err
	}
	raw, err := plain.Load(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "default limits type=%T\n", raw.Map["limits"]); err != nil {
		return err
	}
	p, err := azappconfig.New(selection, client, azappconfig.WithJSONValues())
	if err != nil {
		return err
	}
	type Config struct {
		Server struct {
			Host string `json:"host"`
		} `json:"server"`
		Limits struct {
			RPS     int  `json:"rps"`
			Enabled bool `json:"enabled"`
		} `json:"limits"`
	}
	state, err := fastconf.Load[Config](ctx, fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p), fastconf.WithUnknownFields(fastconf.UnknownError))
	if err != nil {
		return err
	}
	c := state.Value()
	_, err = fmt.Fprintf(out, "json settings: host=%s rps=%d enabled=%t\n", c.Server.Host, c.Limits.RPS, c.Limits.Enabled)
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		panic(err)
	}
}
