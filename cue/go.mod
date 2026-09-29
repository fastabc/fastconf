// Unified CUE module: combines the former validate/cue/cuelang and
// policy/cue sub-modules. Both share cuelang.org/go so the CUE version
// stays in sync automatically.
module github.com/fastabc/fastconf/cue

go 1.26.0

require (
	cuelang.org/go v0.17.1
	github.com/fastabc/fastconf v1.0.0
)

require (
	github.com/cockroachdb/apd/v3 v3.2.3 // indirect
	github.com/emicklei/proto v1.14.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/protocolbuffers/txtpbfmt v0.0.0-20260916144827-6e6d8ebdba95 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
