---
audit: cascade-inside-settlement
artifact: decision:cascade-inside-settlement
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# The parent-settlement cascade fires inside settlement

Supported. For carry (delegation's settle), `lib/runtime/child_execution.go::SettleFromDelegate` — reached from exactly one call site (`lib/runtime/subgraph_dispatch.go`) — builds and fires the parent-settlement cascade signal (`cascadeSubscribersStaleInTx`, attribute-change emission, wait-set drain) inline in its own body, tagged `@decision: cascade-inside-settlement`. For aggregate (fan-out's settle), `lib/runtime/state_propagation.go::PropagateIfChildAfterTerminal` bundles the same cascade-emission logic (`emitSignalInTxOnce`, attribute-change emission, wait-set drain) inside the function that decides, via `Aggregate()`, whether the parent has just settled; every one of its 7 call sites (across `runner_terminal.go`, `runner_error_policy.go`, `runner_terminal_park.go`, `held_cascade_defer.go`, `instance_kill.go`) invokes this single shared function rather than constructing a parent cascade signal itself, so a call site cannot settle a child without going through the bundled cascade path. `test/scenarios/fanout_strict_cascade_e2e_test.go` and `test/scenarios/fanout_success_cascade_e2e_test.go` prove the aggregate path's downstream cascade fires end-to-end; delegation's carry path is proven by the sub-graph delegation e2e suite invoking `SettleFromDelegate`'s sole call site.
