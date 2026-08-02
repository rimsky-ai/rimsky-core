---
audit: host-agent-per-binding-overrides
artifact: story:host-agent-per-binding-overrides
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:12Z
---

# Per-binding env, args, cwd, and spawn timeout are all honored at exec time

Supported. All 4 override dimensions the story names — env, args, cwd, spawn timeout — are exercised end to end by `test/scenarios/host_agent_per_binding_exec_overrides_test.go`'s `TestHostAgentPerBindingExecOverrides`: the `overrides_applied_at_exec` subtest declares a binding with args, an env var, and a cwd, and asserts the spawned child's actual argv/env/working-directory (read back from the child's own exec log) match; the `short_binding_timeout_bounds_the_wait` subtest declares a binding-level `timeout_seconds` shorter than the global default against a child that never binds its port, and asserts the dispatch fails fast rather than waiting out the (much longer) global timeout; the `no_overrides_still_spawns` subtest confirms the no-override path still succeeds. `lib/runtime/hostagent/spawn.go`'s `handleSpawn` implements the fallback rule (binding value if set, else the spawn's global value) for cwd and ready-timeout that these tests exercise.
