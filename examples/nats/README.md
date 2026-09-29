# nats streaming provider reference

This example retains the tests and interface-based implementation formerly in
`providers/nats`. Supply a real transport adapter before using it.
For application-owned copies, copy `internal/providerutil.Latest` and `Offer`
into your own helper package and update the import; Go's internal boundary
prevents importing those helpers from outside this repository.

Run `go test -race ./examples/nats` from the repository root.
