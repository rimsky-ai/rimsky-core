---
audit: audit-artifact
artifact: story:audit-artifact
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:03:56Z
---

# Inspecting a completed one-shot run's record without re-running it

Supported for both one-shot modes the product offers. Each was driven with a
mixed roster — one leg succeeding, one failing against a third-party executor —
and each left a per-run artifact directory carrying the run's state, its blob
store, and the configuration it used. That inspection is not a re-run was
established rather than assumed: the executor process the run spawned was gone
before anything was read, and the record was read by serving a copy of the
artifact through an ordinary deployment. Both the debugging half and the
verifying half of the benefit came back — the failing instance, its worker node,
and its own error class replayed from the record, alongside the succeeding run's
node and its attribute writeback. Two consecutive reads returned the same event
count, so reading the record does not disturb it. Twenty-three checks, none
failing.
