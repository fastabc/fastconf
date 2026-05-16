# Plan / dry-run

`mgr.Plan().Run(ctx)` executes the same assemble + pipeline stages as a reload,
but it does **not** swap the live `State[T]`. Use it in CI, operator tooling, or
deployment approval flows when you want to inspect the proposed state first.

```go
result, err := mgr.Plan().
    WithHostname("prod-eu-1"). // optional: pin host overlays on CI runners
    Run(ctx)
if err != nil {
    log.Fatal(err)
}

for _, line := range result.Diff {
    fmt.Println(line)
}
for _, report := range result.Validators {
    fmt.Printf("validator=%s err=%v\n", report.Name, report.Err)
}
for _, finding := range result.Policies {
    fmt.Printf("policy=%s severity=%v path=%s\n",
        finding.Rule, finding.Severity, finding.Path)
}

if len(result.Diff) > 0 && approveInteractively(result) {
    _ = mgr.Reload(ctx)
}
```

`PlanResult[T]` carries:

| Field | Meaning |
|---|---|
| `Proposed *State[T]` | The candidate state that would be published by a real reload |
| `Diff []string` | Stable dotted-path diff against the current live state |
| `Validators []ValidatorReport` | Validator findings collected during the dry-run |
| `Policies []policy.Violation` | Policy warnings and denials collected for inspection |

Unlike a real reload, `Plan` records `SeverityError` policy findings instead of
publishing a new state. That makes it useful as a deployment gate: inspect the
candidate, then call `Reload` only after approval.
