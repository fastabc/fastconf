//go:build ignore

package main

import (
	"fmt"

	adapter "github.com/fastabc/fastconf/integrations/cli/pflag"
	"github.com/spf13/pflag"
)

func main() {
	flags := pflag.NewFlagSet("example", pflag.ContinueOnError)
	flags.Int("server.port", 8080, "server port")
	flags.String("server.host", "localhost", "server host")
	if err := flags.Parse([]string{"--server.port=9090"}); err != nil {
		panic(err)
	}
	// Only explicitly set flags override configuration; server.host is omitted.
	// Pass this map to cliflag.NewCLI, then fastconf.WithProvider.
	fmt.Println(adapter.FromChanged(flags))
}
