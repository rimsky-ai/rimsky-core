---
audit: fan-out-and-delegation-are-distinct-mechanisms
artifact: decision:fan-out-and-delegation-are-distinct-mechanisms
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:15Z
---

# Fan-out and sub-graph delegation share a thin dispatch helper but keep structurally different inputs and separate settle primitives

Supported. The shared helper is `DispatchChildren` (`lib/runtime/child_execution.go`), taking a partitions × children matrix. `dispatchFanOutChildren` (`lib/runtime/fanout_dispatch.go`) calls it with N partitions (one per sub-claim) × exactly 1 child (the cloned calling node, same `NodeID`), while `applyTerminalCompleteSubgraphCaller` (`lib/runtime/subgraph_dispatch.go`) calls it with exactly 1 partition (empty key) × N children (the sub-graph's distinct internal node types). Settlement is genuinely split into two named primitives: `SettleFromDelegate` (carry — copies the exit's writeback verbatim onto the parent attribute row, fires once) and `SettleFromFanoutChild` (aggregate — records each clone's outcome on the parent claim-handle and only resolves once every holder/child claim is inactive, applying the four-value aggregation policy in `aggregateParentOutcome`). Both call paths are exercised end to end by `test/scenarios/template_fan_out_e2e_test.go` and `test/scenarios/template_sub_graph_delegation_e2e_test.go` respectively, confirming the structurally different matrices and settle primitives are live, not just declared.
