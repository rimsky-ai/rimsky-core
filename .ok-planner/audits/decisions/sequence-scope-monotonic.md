---
audit: sequence-scope-monotonic
artifact: decision:sequence-scope-monotonic
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:42Z
---

# Whether a node-run's sequence is assigned per (node, run-scope) and sibling scopes number independently

Supported. Enumerating every statement in either driver that inserts a node-run row outside test code — twelve in all, six per driver, covering the cascade-pending creation, the queue enqueue, the two run-tree child creations, the non-cascade stale creation, and the source-node-stale marking — each one computes the new sequence as the greatest existing sequence for the same node and run-scope pair plus one, with no exception and no variant keyed on frame or on node alone. Nothing anywhere updates the column after insert, and the column is declared not-null with no default in both schemas, so an insert that forgot to assign it would fail rather than silently take zero. The consequence the decision claims for sibling scopes is exercised: the per-scope latest-run lookup orders by sequence descending within one scope, and the parity suite drives five scope-keyed sequence queries — prior run by sequence, later-pending detection, prior-stale deletion, most-recent-settled, and advanced-sibling detection — across two sibling scopes holding runs of the same node, asserting each answers from its own scope only, against both drivers. No test asserts the assigned number itself, so that half rests on the reading of the twelve statements.
