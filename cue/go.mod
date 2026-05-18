// Unified CUE module: combines the former validate/cue/cuelang and
// policy/cue sub-modules. Both use cuelang.org/go v0.10.0 and are kept
// in the same module so the CUE version stays in sync automatically.
module github.com/fastabc/fastconf/cue

go 1.26.2

require (
	cuelang.org/go v0.10.0
	github.com/fastabc/fastconf v0.0.0
)

require (
	github.com/cockroachdb/apd/v3 v3.2.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/fastabc/fastconf => ..
