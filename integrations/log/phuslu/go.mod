// Separate module so the phuslu/log dependency does not enter the
// dependency closure of plain fastconf users or zerolog adapter users.
module github.com/fastabc/fastconf/integrations/log/phuslu

go 1.25.0

require (
	github.com/fastabc/fastconf v1.5.0
	github.com/phuslu/log v1.0.138
)
