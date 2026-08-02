---
issue: intx-suffix-live-wrapper-pairs-exist
kind: audit
category: decision-drift
artifacts:
  - decision:intx-suffix-convention
status: repaired
opened: 2026-08-02T09:58:22Z
---

# Do any live public-wrapper/private-`...InTx` pairs violate `decision:intx-suffix-convention`'s forbidden-pairing rule?

Yes, two: `resolveAcqScope`/`resolveAcqScopeInTx` (`lib/runtime/runner_acquire_scope.go`) and `sendCascadeMessage`/`sendCascadeMessageInTx` (`lib/runtime/runner_send_message.go`). Both were the exact forbidden shape — a public function that opens its own transaction and delegates to a private, identically-behaved `...InTx` sibling with no other caller.

Rule that determined the fix: outcome-2 code-side repair — the decision spells out the required end state exactly ("one method taking an optional transaction parameter... nil opens its own, non-nil reuses the caller's"), leaving no design choice; only how the commitment is expressed in code changes, not the commitment itself.

Changed: both functions collapsed into a single signature taking a trailing `tx persistence.Tx` (nil opens its own transaction, non-nil reuses the caller's), with the old `...InTx` bodies renamed to unexported single-purpose helpers (`resolveAcqScopeRow`, `sendCascadeMessageRow`) called only from within the merged function. All production call sites (12 for `resolveAcqScope`, 1 for `sendCascadeMessage`) updated to pass `nil`, preserving existing behavior exactly since none previously threaded an outer transaction through. Test call sites in `lib/runtime/runner_send_message_test.go` updated to call the merged `sendCascadeMessage(..., tx)` in place of the retired `sendCascadeMessageInTx`.

Verified: `go build ./...` and `go test ./lib/runtime/...` (including `hostagent`, `claimproducer`, `executor`, `scheduler` subpackages) all pass.
