---
audit: attribute-carry-forward
artifact: decision:attribute-carry-forward
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:03:38Z
---

# Attribute hydration carries the prior same-scope run's bag forward, then overlays source substitution

Supported on every clause. The prior-run lookup is scoped by run-scope in both persistence backends — the query joins the attribute row to its node-run and filters on the same node and the same run-scope, ordering by sequence — so cross-RunScope hydration is structurally impossible rather than merely avoided; a fresh sub-graph or fan-out scope finds no prior row and inserts an empty bag, after which schema defaults fill it. Both backends implement the operation and share one conformance suite that asserts the scope-keyed isolation, so the claim holds for the two stores that exist. The overlay order is exactly as claimed: hydration copies the carried bag first, then walks the schema's properties and overwrites only those declaring a source, while a property carrying only a static default is filled solely when absent — executor-written read-only properties declare neither a source nor a default and therefore survive untouched until the next writeback replaces them. The claim of no opt-in flag was checked by searching the whole template and spec surface for any carry-forward knob; there is none, so the behavior is uniform across all attribute properties by construction. Scenario coverage spans carry-forward inside a run-scope with a sub-graph then seeing schema defaults, fresh-scope defaults at frame start, and sequential runs within one scope.
