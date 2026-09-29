// Separate module so the phuslu/log dependency does not enter the
// dependency closure of plain fastconf users or zerolog adapter users.
module github.com/fastabc/fastconf/integrations/log/phuslu

go 1.24.0

require (
	github.com/fastabc/fastconf v1.0.0
	github.com/phuslu/log v1.0.136
)
