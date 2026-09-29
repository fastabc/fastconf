// Separate module so the zerolog dependency does not enter the
// dependency closure of plain fastconf users or phuslu adapter users.
module github.com/fastabc/fastconf/integrations/log/zerolog

go 1.26.0

require (
	github.com/fastabc/fastconf v1.0.0
	github.com/rs/zerolog v1.35.1
)

require (
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	golang.org/x/sys v0.48.0 // indirect
)
