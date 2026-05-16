// Separate Go module so users that only want the in-process bus can
// avoid pulling NATS-related types into their build graph. This
// sub-module itself depends only on the fastconf root module (for
// contracts.Provider / Event / Codec / SnapshotProvider).
module github.com/fastabc/fastconf/providers/nats

go 1.26.2

require github.com/fastabc/fastconf v0.0.0

replace github.com/fastabc/fastconf => ../..
