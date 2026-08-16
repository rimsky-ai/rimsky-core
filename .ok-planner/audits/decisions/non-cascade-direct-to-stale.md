---
audit: non-cascade-direct-to-stale
artifact: decision:non-cascade-direct-to-stale
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:52:00Z
---

# Non-cascade re-runs created directly in the dispatchable state

Unsupported on one clause, against a Choice that otherwise holds throughout. All three paths do what the decision says: the shared creation primitive inserts the row already in the dispatchable state, requires a creation reason, and refuses a closed run scope, and each of the three production callers passes its own reason — operator-invalidate from the debug-override entry point, recalculate from the fan-out parent path, and message-delivery from the delivery path. The bag is computed at row creation, not later: the primitive snapshots it from the immediately prior run of the same node in the same run scope, falling back to an empty bag when none exists, and the message-delivery caller then overwrites both the live bag and the dispatch input bag with the envelope payload verbatim, which is right because a fresh frame's root scope has no prior. The sequence is a fresh maximum-plus-one per node and run scope computed in the same insert. The immunity claim holds by construction — all four walker and mode-rule queries restrict to the cascade creation reason, so a non-cascade row is never an accumulation target, never deleted by most-recent, and never dropped by the idempotent variants — and policy-retry is indeed in-place, stamping the existing row with itself as predecessor and creating nothing. The clause that fails is the ordering one: candidate selection in both storage dialects orders by enqueue timestamp and then by row id and never reads the sequence column, so while the serialisation gate is genuinely the same one that governs cascade-driven rows, nothing claims these rows in sequence order.
