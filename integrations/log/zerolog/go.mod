// Separate module so the zerolog dependency does not enter the
// dependency closure of plain fastconf users.
module github.com/fastabc/fastconf/integrations/log/zerolog

go 1.26.2

require github.com/rs/zerolog v1.33.0

require (
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace github.com/fastabc/fastconf => ../../..
