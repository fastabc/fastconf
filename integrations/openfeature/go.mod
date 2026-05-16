// Separate Go module so users that only want native FastConf APIs do
// not pull go-sdk-openfeature into their dependency closure. This
// sub-module accepts an OpenFeature-shaped EvaluationContext (a small
// map[string]string) and routes it through Manager.Eval — no external
// dependency is required at this layer.
module github.com/fastabc/fastconf/integrations/openfeature

go 1.26.2

require github.com/fastabc/fastconf v0.0.0

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/fastabc/fastconf => ../..
