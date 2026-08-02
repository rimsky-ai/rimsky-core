---
audit: claim-producer-conformance
artifact: story:claim-producer-conformance
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# The claim-producer conformance runner drives the four terminal verbs, idempotency, and the 9b serialization probe

Supported. `lib/protocols/conformance/claimproducer/terminals.go` drives Commit, Abandon, and Release each as a standalone check plus a dedicated idempotency check per verb (`checkTerminalIdempotency`) that calls the same verb twice on one claim and fails if the retry is rejected — matching the "idempotency under retry" claim for all three terminal verbs (Open's own uniformity/envelope checks live alongside in `runner.go`). `serialization9b.go` implements the staged-async dishonest-serialization probe: it opens a writer, opens two concurrent readers on the byte-equal scope, and fails if either reader blocks or comes back unavailable, skipping cleanly when the producer doesn't advertise staged_async. Each check reports a pass/fail `CheckResult` by name. The suite is wired into the CLI as `rimsky conformance claim-producer` (`cmd/rimsky/conformance.go`, exercised by `cmd/rimsky/conformance_claimproducer_test.go` and `conformance_claimproducer_cli_test.go`), satisfying the "run the conformance suite against my producer endpoint" framing.
