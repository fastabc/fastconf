# Preview, reject, commit, and roll back

Run from the repository root (Go 1.24+, no services required):

```sh
go run ./examples/reload
```

```text
invalid preview: violations=1 live=8080
rejected: preserved=true
policy preview: rejected=true live=8080
content changed=true warnings=1
valid preview: proposed=9090 live=8080 changes=1
committed: port=9090 generation=2
rollback: port=8080 generation=3
unchanged source: port=9090 generation=4
source repaired: port=8080 generation=5
```

The program copies [initial.yaml](config/initial.yaml) to a temporary local
file, then tries [invalid.yaml](config/invalid.yaml),
[policy-invalid.yaml](config/policy-invalid.yaml) and
[proposed.yaml](config/proposed.yaml). It removes the temporary directory on
exit, leaving repository fixtures unchanged.

`Plan` collects field validation findings without publishing. A nil Plan error
does not mean the candidate passes validation: inspect `Validators[].Err` and
`Policies[].Severity`. Policy evaluation errors are also collected as findings.
Malformed input and failures before validation can still return an error. `Reload` rejects invalid fields and preserves the exact
previous snapshot. A valid reload publishes generation 2; rollback republishes
the initial snapshot as generation 3 without rerunning validation.

There is no automatic watcher, so the example controls publication. `Reload`
loads sources again; a preview does not freeze them or reserve the approved
candidate. Rollback restores in-memory state and leaves the source file unchanged,
so a later reload would load port 9090 again.

Related: [Plan](../../docs/cookbook/runtime/plan.md),
[diagnostics and history](../../docs/cookbook/runtime/introspect.md).

The port policy rejects 7070 and warns for 9090. `checkPlan` checks validator
errors and error-severity policies; `printChange` compares hashes as well as
diagnostic entries, so JSON-hidden YAML changes are not labeled unchanged.
The final steps first demonstrate the unchanged source reapplying 9090, then
repair it to 8080 and validate before publication.

Source: [main.go](main.go).

Verify from the repository root: `go test -race ./examples/reload`.
