// fastconfgen generates Go structs from sample YAML. It does not depend on
// the configuration runtime, so it lives in its own module and version
// line: `go install github.com/fastabc/fastconf/cmd/fastconfgen@latest`.
module github.com/fastabc/fastconf/cmd/fastconfgen

go 1.24.0

require gopkg.in/yaml.v3 v3.0.1
