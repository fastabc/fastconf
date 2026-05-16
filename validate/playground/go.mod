// Separate Go module so go-playground/validator and its transitive
// closure (mimetype, locales, universal-translator, leodido/go-urn,
// golang.org/x/crypto, golang.org/x/text) do not enter the dependency
// graph of plain fastconf users. Importers that want struct-tag based
// validation must `go get github.com/fastabc/fastconf/validate/playground`
// in addition to the root module.
module github.com/fastabc/fastconf/validate/playground

go 1.26.2

require github.com/go-playground/validator/v10 v10.30.2

require (
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/stretchr/testify v1.9.0 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
)

replace github.com/fastabc/fastconf => ../..
