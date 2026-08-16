---
audit: delegation
artifact: concept:delegation
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:34Z
---

# Sub-graph delegation: entry absorption, carry settlement, and the eight invariants the concept claims

Supported. All eight invariants hold. Template canonicalisation absorbs the entry into the calling node deterministically, inheriting exactly the four things the concept names — executor, claim-producer bindings, holds bindings, and attribute schema, each merged with what the caller declared — and rejects a node that declares both an executor and a delegate target. A delegate target that names no graph declaring both an entry and an exit is rejected at registration under the error identifier the concept names, with unit tests on both the rejecting and accepting sides, and the runtime helper that resolves a sub-graph's internals errors on an unknown graph as the backstop. The entry keeps its own node row but is filtered out of the internal-node set the dispatch helper receives, so no entry node-run is ever created; internal subscriptions naming the entry alias are flagged at canonicalisation and bound at runtime to the calling node's run as sender, and the caller's merged bag is seeded onto entry-alias-sourced internal attributes, which is the live half of that invariant. Delegation dispatches one partition by N children and its carry settle fires once on the exit's terminal, copying the writeback onto the caller's row, closing the child scope, and firing the parent-settlement cascade in one transaction. The caller's own claims are resolved on both outcomes — committed on the carried exit success, abandoned on a settled sub-graph failure — through one shared resolver reached from each path. Delegate and fan-out are not mutually exclusive: the validator rejects only the executor-plus-delegate and delegate-plus-sends-message pairs, and a scenario test drives a fan-out node that delegates.
