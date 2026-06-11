// Separate module so the spf13/pflag dependency does not enter the
// dependency closure of plain fastconf users.
module github.com/fastabc/fastconf/integrations/cli/pflag

go 1.22

require (
	github.com/fastabc/fastconf v0.19.2
	github.com/spf13/pflag v1.0.10
)
