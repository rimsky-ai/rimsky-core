---
audit: advisory-lock
artifact: concept:advisory-lock
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:46:57Z
checked: 5
unaccounted: 0
---

# Five advisory-lock primitives, both backends, and the invariants governing their scope

Supported. The persistence-layer advisory-locker interface declares exactly the five primitives the concept names — scheduler-tick, migration, per-name, per-claim-scope, per-lifecycle-scope — and both shipped backends implement all five. The client-server backend uses a non-blocking session try-lock for the tick, a blocking session lock held for the whole migration batch, and transaction-scoped locks for the other three; the embedded backend derives two lock files from the database path (non-blocking exclusive for the tick, poll-until-acquired for migration) and makes the three in-tx primitives no-ops, with a test proving the immediate-mode write transaction closes the read-then-write window the no-op gives up. The two pinned keys are two distinct constants. The tick's lock-error path skips the sweep rather than running unlocked, and a test drives that with an always-erroring locker. Multi-lock acquisition sorts specs by lock kind then sort key before taking any lock, and a scenario test asserts the resulting order. The lifecycle fan-out wraps lock, idempotency check, delivery, and mark in one transaction, which is what makes the embedded backend's no-op sound; the shared persistence conformance suite runs the tick, lifecycle-scope serialization, and concurrent-acquisition cases against both backends.
