// Example schema prints generated artifacts; applications can write each to a
// file from an internal generator invoked by go generate.
package main

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"github.com/fastabc/fastconf/schema"
)

type Config struct {
	Port     int           `json:"port" default:"8080" desc:"HTTP listen port" fc:"min=1,max=65535"`
	Timeout  time.Duration `json:"timeout" default:"5s" desc:"Request timeout"`
	Database struct {
		DSN   string   `json:"dsn" fc:"required,secret" desc:"Database DSN"`
		Hosts []string `json:"hosts" default:"a,b"`
	} `json:"database"`
}

func example(w io.Writer) error {
	t := reflect.TypeFor[Config]()
	js, err := schema.JSONSchema(t)
	if err != nil {
		return err
	}
	yml, err := schema.ExampleYAML(t)
	if err != nil {
		return err
	}
	env, err := schema.ExampleEnv(t, "APP_", "__")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "--- config.schema.json ---\n%s\n--- config.example.yaml ---\n%s--- .env.example ---\n%s", js, yml, env)
	return err
}
func main() {
	if err := example(os.Stdout); err != nil {
		panic(err)
	}
}
