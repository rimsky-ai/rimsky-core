---
audit: host-agent-per-run-scope-isolation
artifact: story:host-agent-per-run-scope-isolation
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:12Z
---

# Sibling fan-out run-scopes get distinct spawned children, reaped independently

Supported. `test/scenarios/host_agent_per_run_scope_isolation_test.go`'s `TestHostAgentPerRunScopeIsolation` runs a 2-partition fan-out (`alpha`, `beta`) against a late-bound executor and asserts, by reading back a pid-per-run-scope log the spawned stub writes, that the 2 partition run-scopes are served by disjoint, non-overlapping pid sets — including after a later best-effort re-dispatch, so isolation isn't just a startup artifact. `TestHostAgentPerRunScopeReapIsolation` dispatches into 2 independent run-scopes directly, confirms they get 2 distinct child pids, closes one run-scope via the lifecycle-subscriber terminal callback, and asserts that scope's child dies while the sibling's child stays alive — proving reap is scoped to the terminated run-scope, not global. Both tests are real end-to-end runs against a real host-agent and real spawned processes, matching the story's "own isolated child process" and "reaped when its run-scope closes" claims.
