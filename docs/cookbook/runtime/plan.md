# Plan / dry-run

`mgr.Plan(ctx)` executes the same assemble + pipeline stages as a reload,
but it does **not** swap the live `State[T]`. Use it in CI, operator tooling, or
deployment approval flows when you want to inspect the proposed state first.

Providers, generators and user hooks still run, so a preview can perform I/O or
other effects in those extensions. It only guarantees no snapshot publication.
`WithOverride` is a ReloadOption and cannot be passed to Plan.

```go
before := mgr.Snapshot()
result, err := mgr.Plan(ctx,
    fastconf.WithPlanHostname("prod-eu-1"), // optional: pin host overlays on CI runners
)
if err != nil {
    log.Fatal(err)
}

for _, line := range fastconf.FormatDiff(result.Diff) {
    fmt.Println(line)
}
acceptable := true
for _, report := range result.Validators {
    fmt.Printf("validator=%s err=%v\n", report.Name, report.Err)
    if report.Err != nil {
        acceptable = false
    }
}
for _, finding := range result.Policies {
    fmt.Printf("policy=%s severity=%v path=%s\n",
        finding.Rule, finding.Severity, finding.Path)
    if finding.Severity == policy.SeverityError {
        acceptable = false
    }
}

changed := before.Hash() != result.Proposed.Hash()
if changed && len(result.Diff) == 0 {
    fmt.Println("configuration changed; no visible JSON field differences")
}
if acceptable && changed && approveInteractively(result) {
    if err := mgr.Reload(ctx); err != nil {
        return err
    }
}
```

`PlanResult[T]` carries:

| Field | Meaning |
|---|---|
| `Proposed *State[T]` | The candidate state that would be published by a real reload |
| `Diff []DiffEntry` | Structured dotted-path changes against the current live state; use `FormatDiff` for text |
| `Validators []ValidatorReport` | Validator findings collected during the dry-run |
| `Policies []policy.Violation` | Policy warnings and denials collected for inspection |

Unlike a real reload, `Plan` records `SeverityError` policy findings instead of
publishing a new state. That makes it useful as a deployment gate: inspect the
candidate, then call `Reload` only after approval.

A nil Plan error does not imply valid findings. Policy evaluation errors become
error-severity findings in `Policies`. Assembly, transform, secret resolution,
typed conversion and decode failures still abort the preview. `Reload` loads inputs again;
the preview does not freeze files or remote values, and an active watcher can
publish independently of the preview flow.

## Runnable example

[`examples/reload`](../../../examples/reload/README.md) supplies local YAML for an
invalid candidate and a valid replacement, then demonstrates rejection, commit
and rollback. Run `go run ./examples/reload` from the repository root.

`approveInteractively` above represents the application's approval UI. The
runnable example implements a deterministic scenario without that placeholder.
Compare a retained before snapshot with Proposed; an active watcher can change
the live state independently. Pause does not drain queued work or freeze inputs.

## Recovery

Stop external publishers, choose a retained known-good snapshot, roll back, then
repair the source and Plan/Reload it before resuming updates. History excludes
the current snapshot and is oldest-first. Rollback does not revalidate, restore
expired credentials, repair a source, or undo a connection-pool change. If old
credentials or policies no longer apply, repair the source and use the normal
validated reload path. The runnable example demonstrates why an unchanged source
reapplies the configuration that was rolled back.
