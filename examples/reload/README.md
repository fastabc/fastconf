# Preview, reject, commit, and roll back

Run from the repository root (Go 1.24+, no services required):

```sh
go run ./examples/reload
```

```text
invalid preview: violations=1 live=8080
rejected: preserved=true
valid preview: proposed=9090 live=8080 changes=1
committed: port=9090 generation=2
rollback: port=8080 generation=3
```

The program copies [initial.yaml](config/initial.yaml) to a temporary local
file, then replaces it with [invalid.yaml](config/invalid.yaml) and
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

Related: [Plan](../../docs/cookbook/plan.md),
[diagnostics and history](../../docs/cookbook/introspect.md).
