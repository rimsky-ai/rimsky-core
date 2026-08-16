---
audit: executor-reads-dispatch-context
artifact: story:executor-reads-dispatch-context
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:10:09Z
---

# A script reads its dispatch identity and its predecessor's disposition, and adapts on it

Supported. Driven through the public surface against a released-image stack
running the bundled agent executor with a stand-in agent binary that reads the
dispatch context and writes what it read into the node's output. Eight checks,
none failing. A fresh dispatch read its own dispatch id — the same id rimsky
recorded for that run — the run-scope it belongs to, and no predecessor. A
dispatch following a reaped quiet predecessor read that predecessor's id and a
stale-recovery disposition, and reached success only on the branch its script
takes when a predecessor is present. A dispatch recalculated by an upstream
fan-out's settlement read a recalculate disposition naming its predecessor, where
that node's first dispatch had read none. Three distinct dispositions were
observed across the run from the dispatch context alone, with no indirect signal
involved.
