package fastconf_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/pkg/provider"
)

type routingLabelsCfg struct {
	Routing struct {
		Enable bool `yaml:"enable" json:"enable"`
		HTTP   struct {
			Routers map[string]struct {
				Entrypoints []string `yaml:"entrypoints" json:"entrypoints"`
				TLS         struct {
					Domains []struct {
						Main string   `yaml:"main" json:"main"`
						Sans []string `yaml:"sans" json:"sans"`
					} `yaml:"domains" json:"domains"`
				} `yaml:"tls" json:"tls"`
			} `yaml:"routers" json:"routers"`
			Services map[string]struct {
				LoadBalancer struct {
					Server struct {
						Port int `yaml:"port" json:"port"`
					} `yaml:"server" json:"server"`
				} `yaml:"loadbalancer" json:"loadbalancer"`
			} `yaml:"services" json:"services"`
		} `yaml:"http" json:"http"`
	} `yaml:"routing" json:"routing"`
}

func TestRoutingLabels_IntegrationDecodesTypedShape(t *testing.T) {
	mgr, err := fastconf.New[routingLabelsCfg](context.Background(),
		fastconf.WithFS(fstest.MapFS{
			"conf.d/base/00-empty.yaml": &fstest.MapFile{Data: []byte("{}\n")},
		}),
		fastconf.WithDir("conf.d"),
		fastconf.WithProvider(provider.NewRoutingLabels([]string{
			"routing.enable=true",
			"routing.http.services.api.loadbalancer.server.port=8080",
			"routing.http.routers.api.entrypoints=web,websecure",
			"routing.http.routers.api.tls.domains[0].main=example.com",
			"routing.http.routers.api.tls.domains[0].sans=www.example.com,api.example.com",
		}, provider.RoutingLabelOptions{})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	got := mgr.Get()
	if !got.Routing.Enable {
		t.Fatal("routing.enable should decode as true")
	}
	if port := got.Routing.HTTP.Services["api"].LoadBalancer.Server.Port; port != 8080 {
		t.Fatalf("port got %d", port)
	}
	router := got.Routing.HTTP.Routers["api"]
	if len(router.Entrypoints) != 2 || router.Entrypoints[0] != "web" || router.Entrypoints[1] != "websecure" {
		t.Fatalf("entrypoints got %#v", router.Entrypoints)
	}
	if len(router.TLS.Domains) != 1 || router.TLS.Domains[0].Main != "example.com" {
		t.Fatalf("domains got %#v", router.TLS.Domains)
	}
	if sans := router.TLS.Domains[0].Sans; len(sans) != 2 || sans[0] != "www.example.com" || sans[1] != "api.example.com" {
		t.Fatalf("sans got %#v", sans)
	}
}
