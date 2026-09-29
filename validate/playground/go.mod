// Separate Go module so go-playground/validator and its transitive
// closure (mimetype, locales, universal-translator, leodido/go-urn,
// golang.org/x/crypto, golang.org/x/text) do not enter the dependency
// graph of plain fastconf users. Importers that want struct-tag based
// validation must `go get github.com/fastabc/fastconf/validate/playground`
// in addition to the root module.
module github.com/fastabc/fastconf/validate/playground

go 1.26.0

require github.com/go-playground/validator/v10 v10.30.5

require (
	github.com/gabriel-vasile/mimetype v1.4.15 // indirect
	github.com/go-playground/locales v0.14.2 // indirect
	github.com/go-playground/universal-translator v0.18.2 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
