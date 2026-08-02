---
audit: proxy-single-spawn-multiplexing
artifact: decision:proxy-single-spawn-multiplexing
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:12Z
---

# Concurrent dispatches share one spawn without head-of-line blocking

Supported. `cmd/rimsky-host-agent-proxy/dispatch_multiplexing_test.go` carries both halves of the claim as dedicated tests: `TestConcurrentDispatchesOnSameSpawnNoHeadOfLineBlocking` starts a slow dispatch that blocks on a channel, waits for its spawn to register, then fires a fast dispatch on the same (run-scope, binding) and asserts the fast one completes and returns before the slow one is released — proving per-dispatch stream identifiers avoid serializing behind a slower sibling. `TestConcurrentDispatchesShareOneSpawn` races 5 concurrent first-dispatches against the same (run-scope, binding) and, via a spawn-observer hook, asserts exactly 1 `Spawn` request was ever issued to the agent — proving the "only one spawn is ever issued" clause under a real race, not just a lock-ordering argument.
