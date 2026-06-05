// Command fastconfctl is a CLI companion to FastConf for CI / ops:
//
//	fastconfctl dump      [-dir conf.d] [-profile prod]
//	fastconfctl diff      [-dir conf.d] -from dev -to prod
//	fastconfctl validate  [-dir conf.d] [-profile prod]
//	fastconfctl explain   [-dir conf.d] [-profile prod] <dotted.path>
//
// The tool reuses the public fastconf engine — no special access to
// internal packages — so its behaviour matches what the application
// would observe at runtime. The `explain` subcommand uses
// WithProvenance(ProvenanceFull) to print the per-path origin chain.
package main

import (
	"fmt"
	"os"
)

const usage = `fastconfctl <command> [flags]

Commands:
  dump      Print the merged configuration as JSON.
  diff      Diff merged configuration between two profiles.
  validate  Run the assemble+merge pipeline and report errors.
  explain   Show the origin chain for a dotted path.
  version   Print the binary version and exit.

Run 'fastconfctl <command> -h' for command-specific flags.`

// version is injected at build time via `-ldflags "-X main.version=<tag>"`
// by the dist pipeline. Default "dev" is reported when building from source
// without -ldflags (e.g. `go install`).
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "dump":
		err = runDump(args)
	case "diff":
		err = runDiff(args)
	case "validate":
		err = runValidate(args)
	case "explain":
		err = runExplain(args)
	case "version", "-v", "--version":
		fmt.Printf("fastconfctl %s\n", version)
		return
	case "-h", "--help", "help":
		fmt.Println(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s\n", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
