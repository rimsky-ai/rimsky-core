---
audit: scratch-column
artifact: decision:scratch-column
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:42Z
---

# Whether executor scratch persists on the node-run row as an inline-or-spilled payload following the blob spill pattern

Supported. The node-run ledger carries a three-column scratch triple — inline bytes, spill handle, handle backend — in both drivers' schemas, present in the initial migration and preserved through the later table-rebuild migration; there is no scratch table anywhere in either migration set, which is the rejected alternative. The write path enforces that inline and handle are mutually exclusive, takes the spill decision through the same shared threshold helper and blob backend the other inert payloads use, and enrols the superseded handle as a blob orphan, so the retention sweep reclaims it. All three columns are nullable with no default, so a freshly created run reads back empty on all three, and the parity suite pins that: a missing-row case asserting the load returns empty inline, empty handle, and empty backend, plus over-threshold and at-threshold round trips driven through a real backend — all of it run against both drivers. The phrase "the row's other inert payloads" is loose in one respect: the only other spilled payload in the schema sits on the per-run attribute ledger, a satellite table keyed one-to-one on the node-run row rather than a column of it. The spill idiom is nonetheless the same one.
