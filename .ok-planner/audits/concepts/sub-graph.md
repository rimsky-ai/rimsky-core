---
audit: sub-graph
artifact: concept:sub-graph
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:34Z
---

# The sub-graph template shape and its seven registration and dispatch rejections

Supported. All seven invariants are enforced, each by a distinct named rejection in the graph validator or the dispatch helper. A graph other than the main graph that omits its entry or its exit is rejected, as is one whose entry and exit name the same node, one whose entry or exit names a node it does not declare, and a main graph carrying either declaration. Internal-reference locality is enforced in both directions: a sub-graph node whose subscribes or holds-from names anything not declared in that same sub-graph is rejected — which covers outer-graph nodes and other sub-graphs' internals alike, while leaving the entry alias legal because the entry is declared in the sub-graph — and main-graph nodes referencing a sub-graph internal are rejected by the mirror check. Connectivity is a two-direction traversal over the subscribes edges: every internal other than the entry and exit must be both reachable from the entry and able to reach the exit, with a separate rejection when the exit itself is unreachable from the entry. Recursion is caught at registration by a cycle walk over the delegate edges between graphs, and the dispatch backstop the concept describes is real and correctly placed — the shared child-dispatch helper walks the parent run-scope's ancestor chain and refuses when the target graph name is already open, before it creates any child scope or child run, and it runs only on the entry-absorbed path, so fan-out partition scopes, which share their graph's name by construction, never reach it.
