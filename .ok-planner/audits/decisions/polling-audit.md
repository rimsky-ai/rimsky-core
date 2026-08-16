---
audit: polling-audit
artifact: decision:polling-audit
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T09:12:00Z
---

# The claimed division of test waits into outcome-polls and event-log-tail blocks

Unsupported. The instrument exists and works as described: a dedicated wait helper matches on the event ledger by instance, node, kind, or kind prefix with a minimum count, and blocks without any deadline until the rows appear, which is the durable-record wait the decision calls for; the harness's own event-count and lineage-count waits have the same unbounded shape. What is not carried is the division the Choice asserts as current fact. Six scenario files use the event-tail helper. Against that, the wall-clock ratchet's baseline records 234 wall-clock verdict idioms still live across 115 test files, and reading into them finds waits that are squarely in the class the decision says should block on the event tail rather than poll — the sub-graph delegation suite, for one, waits on a deadline for an inner node to be observed in a transient in-flight state and then asserts the calling node is held at that moment, a pass that depends entirely on the sampler catching an ordering window a poll can step over. The decision also supplies no way to tell its two classes apart: there is no marker, no lint, and no enumerated population for "a wait whose pass depends on an ordering assumption", so nothing in the tree distinguishes a legitimate outcome-poll from an unconverted ordering wait, and the sibling ratchet that governs the backlog acknowledges it as a backlog rather than an already-settled division.
