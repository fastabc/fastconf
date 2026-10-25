// Command fs loads one embedded document through source.NewFS.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
)

//go:embed app.yaml
var files embed.FS

type Config struct {
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port" fc:"min=1,max=65535"`
	} `json:"server"`
}

func main() {
	// Load and validate one required embedded document without background workers.
	state, err := fastconf.Load[Config](context.Background(),
		fastconf.WithFS(files), // Keep directory discovery inside the embedded FS.
		fastconf.WithProvider(source.NewFS(files, "app.yaml")),
		fastconf.WithUnknownFields(fastconf.UnknownError),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Read the typed result after the complete load succeeds.
	cfg := state.Value()
	fmt.Printf("host=%s port=%d\n", cfg.Server.Host, cfg.Server.Port)
}
