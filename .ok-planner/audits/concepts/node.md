---
audit: node
artifact: concept:node
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:57:08Z
---

# The node identity row: no runtime state, immutable during frame processing, four-bucket projection

Supported, and the central invariant is stronger in the code than the prose claims. The node identity row has no update statement at all — zero across both backends — so "set once at instance creation and never updated thereafter" is a structural fact rather than a discipline; a migration removed the two columns that frame processing used to write, the per-frame owning pointer and the update stamp, leaving identity, executor, operator tags, cascade mode, and a creation stamp. Nothing on the row is a lifecycle phase, a policy cursor, a retry counter, or an in-flight marker: those live on the per-run row and the per-run attribute row, both created inside the dispatching frame. The categorical projection is the four counts the concept names, and the switch that builds it partitions the seven run states exactly as stated — running, held, and parked into active; pending and stale into pending; the two terminal states into their own counts — so every run contributes to exactly one bucket. Tag substitution at materialization is handed a resolve context carrying instance params and no other source, rejects a tag that resolves to a non-string, and its failure aborts instance creation; tags are written to the row and read nowhere in dispatch, cascade, or validation. Kind sugar resolves through a registration-time alias map that rewrites the alias to an executor reference and leaves the node's type untouched, and the registration validator rejects a node declaring both a kind and an executor, both a kind and a delegate, and both a kind and a message send, with unknown executors rejected by the declared-executor hook on the same pass.
