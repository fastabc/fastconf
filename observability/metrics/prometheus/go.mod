// Separate Go module so the prometheus client library only enters the
// dependency closure of users that explicitly import this subpackage.
// The parent fastconf module stays zero-dependency on Prometheus.
module github.com/fastabc/fastconf/observability/metrics/prometheus

go 1.26.2

require github.com/prometheus/client_golang v1.20.5

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	golang.org/x/sys v0.42.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)
