// Package fastconf loads layered configuration into immutable typed snapshots.
// LoadFile reads one required file; Load returns a diagnostic State without workers;
// New starts a Manager with serialized reloads and lock-free, zero-allocation Get.
//
// Every failed pipeline preserves the published snapshot. Errors wrap ErrNoSources,
// ErrDecode, ErrMerge, ErrTransform, ErrProvider, ErrInvalid, ErrClosed or ErrTooLarge;
// context cancellation remains recognizable. Map, Dump, Diff and Explain mask secrets;
// Unredacted is the explicit plaintext path.
//
// Use Subscribe for commits, Manager.Errors for failures, Plan for previews and
// History for retained snapshots. See ExampleNew, ExampleSubscribe,
// ExampleManager_Errors, ExampleManager_Plan and ExampleHistory_Rollback.
//
// Runtime behavior is defined in docs/design/spec.md. Module boundaries are listed
// in docs/architecture.md; run tools/modules.sh list for the current inventory.
package fastconf
