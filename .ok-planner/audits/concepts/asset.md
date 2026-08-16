---
audit: asset
artifact: concept:asset
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:09:41Z
---

# The asset compound, its per-instance identity, and its presentation surface

Supported. The compound is exactly the one the concept defines and is nowhere a primitive: every asset read resolves by listing the instance's claim handles filtered to committed state and durable lifetime, then dropping any whose producer does not advertise the data-processing capability, so a durable committed claim against a producer lacking that capability stays in the ledger but never surfaces — covered by a test that asserts it is invisible to both detail and delete, and by a companion asserting an absent producer registry fails closed rather than listing every claim as an asset. Identity is per-instance and composed as the concept says: every route, CLI verb, and tool call is scoped to an instance id and addresses the asset by the node type joined to the claim alias, with cross-instance isolation exercised end to end. The template's claim declaration carries exactly six fields, five of them the rimsky-aware ones the concept enumerates and the sixth an opaque raw-JSON payload rimsky never parses. Delete does what the invariant says and in that order: it locks the handle, refuses when any holder is still active, calls the producer's release verb, then deletes the row under a guard that re-checks for active holders, logging a reconciliation alarm if the row survives the release. The presentation surface exists across all three operator interfaces — five REST routes, five CLI verbs, five MCP tools — split across a read grant and a separate delete grant, with a dry-run preview on delete. No materialize verb exists anywhere in the tree; re-materialization is driven by posting a message to the instance, as the boundary states, and is exercised as such.
