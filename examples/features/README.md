# Feature targeting and percentage rollout

Run from the repository root (Go 1.24+, no services required):

```sh
go run ./examples/features
```

```text
alice checkout=true stable=true
bob checkout=false stable=true
carol checkout=true stable=true
missing flag=false missing anchor=false
```

The complete [feature document](conf.d/base/00-features.yaml) enables checkout
for enterprise users first, then rolls it out to 30% of `user.id` buckets.
Targets precede rollouts. Requests without a matching target or a non-empty
rollout anchor receive the rule default; missing flags receive the caller's
fallback. The same user ID always lands in the same bucket.

The example embeds YAML and loads it once. A service can use `New` and evaluate
`mgr.Get().Features` on each request to pick up committed changes. Snapshot
maps and rules remain read-only. Hashing uses the anchor alone; include a flag
namespace in the anchor when independent flag cohorts are needed.

Related: [feature cookbook](../../docs/cookbook/features.md).
