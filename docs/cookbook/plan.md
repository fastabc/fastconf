# Plan / dry-run

`mgr.Plan(ctx)` executes the same assemble + pipeline stages as a reload,
but it does **not** swap the live `State[T]`. Use it in CI, operator tooling, or
deployment approval flows when you want to inspect the proposed state first.

Providers, generators and user hooks still run, so a preview can perform I/O or
other effects in those extensions. It only guarantees no snapshot publication.
`WithOverride` is a ReloadOption and cannot be passed to Plan.

```go
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

if acceptable && len(result.Diff) > 0 && approveInteractively(result) {
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

[`examples/reload`](../../examples/reload/README.md) supplies local YAML for an
invalid candidate and a valid replacement, then demonstrates rejection, commit
and rollback. Run `go run ./examples/reload` from the repository root.
