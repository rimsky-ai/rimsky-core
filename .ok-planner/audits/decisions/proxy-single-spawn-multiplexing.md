---
audit: proxy-single-spawn-multiplexing
artifact: decision:proxy-single-spawn-multiplexing
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# One spawn per run-scope-and-binding under a race, and per-dispatch stream identifiers

Supported. Resolution first looks up an existing spawn for the run-scope-and-binding pair and returns it; on a miss it enters a single-flight group keyed by that same pair, re-checks inside the critical section, and only then issues the spawn, so several dispatches racing to be first against one binding produce exactly one spawn and one set of the binary's startup side effects. Every dispatch — executor executes and all four claim-producer verbs alike — mints a fresh stream identifier, registers its own response channel under it, and the agent side routes each inbound frame to that channel and runs each call in its own goroutine, so a slow in-flight call cannot block a faster one over the shared connection. Two tests in the proxy package drive it end to end: concurrent dispatches against one spawn with the slow one held open while the fast one completes, and concurrent dispatches asserted to share a single spawn; a state-level test covers the run-scope-and-binding deduplication directly. Neither rejected alternative is present: nothing spawns per dispatch, and no serialization point exists between dispatches on one binding.
