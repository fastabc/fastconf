package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/fastabc/fastconf/cmd/internal/cli"
)

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	var f cli.Flags
	cli.RegisterFlags(fs, &f)
	_ = fs.Parse(args)
	if _, err := loadDump(f); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		return err
	}
	fmt.Println("OK")
	return nil
}
