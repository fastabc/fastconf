package contracts

import "context"

// Snapshot is the richer return type used by SnapshotProvider. It augments
// the legacy Load() map with two additional pieces of metadata that align
// FastConf's reload pipeline with etcd / Vault / Consul-style versioning:
//
//   - Revision is an opaque, monotonically-meaningful version string (e.g.
//     etcd revision, Vault KV current_version, Consul ModifyIndex). The
//     framework records it per-provider and skips the rest of the pipeline
//     when every provider's Revision is unchanged AND no file layer mutated.
//   - Stale means "this snapshot is best-effort and the provider could not
//     verify it is fresh" (degraded read from a cache after the upstream
//     went down). The framework logs a warning and forwards Stale through
//     ReloadCause so audit pipelines can surface the degradation.
//
// Map has identical semantics to Provider.Load — it MUST NOT be retained or
// mutated by the caller; the provider remains the owner.
type Snapshot struct {
	Map      map[string]any
	Revision string
	Stale    bool
}

// SnapshotProvider is an optional extension to Provider. Providers that can
// expose a revision SHOULD implement it; Manager will prefer LoadSnapshot
// over Load when both are available. Providers that only implement Load are
// transparently adapted by the framework via Snapshot{Map: m}.
type SnapshotProvider interface {
	Provider
	LoadSnapshot(ctx context.Context) (Snapshot, error)
}
