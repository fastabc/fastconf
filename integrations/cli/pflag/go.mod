// Separate module so the spf13/pflag dependency does not enter the
// dependency closure of plain fastconf users.
module github.com/fastabc/fastconf/integrations/cli/pflag

go 1.26.2

replace github.com/fastabc/fastconf => ../../..

require (
	github.com/fastabc/fastconf v0.0.0-00010101000000-000000000000
	github.com/spf13/pflag v1.0.10
)
