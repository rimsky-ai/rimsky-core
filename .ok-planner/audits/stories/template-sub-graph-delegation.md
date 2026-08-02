---
audit: template-sub-graph-delegation
artifact: story:template-sub-graph-delegation
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:15Z
---

# A node declaring a `delegate` target dispatches the named sub-graph and settles when it settles

Supported. `applyTerminalCompleteSubgraphCaller` (`lib/runtime/subgraph_dispatch.go`) resolves the sub-graph's internal nodes (excluding the absorbed entry) and dispatches them as one shared child run-scope via the common `DispatchChildren` helper; `SettleFromDelegate` carries the designated exit's writeback back onto the calling node's attribute row and fires the parent-settlement cascade. `TestTemplateSubGraphDelegation_SuccessPropagates` (`test/scenarios/template_sub_graph_delegation_e2e_test.go`) declares a `caller` node delegating to a `worker` sub-graph with an internal entry → mid → exit chain, holds each internal node's dispatch until released, and asserts the sub-graph's run-scope and its internal nodes materialize and drive to completion before the calling node settles — directly exercising the compose-from-reusable-units claim end to end.
