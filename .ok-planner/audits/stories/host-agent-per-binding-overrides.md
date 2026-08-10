---
audit: host-agent-per-binding-overrides
artifact: story:host-agent-per-binding-overrides
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# All four per-binding overrides reach the spawned process

Supported. All four things the story names — environment, arguments, working
directory and spawn timeout — were measured, each on a binding declared in an
instance against a containerised deployment with an agent connected from the
host. Two bindings ran the same binary under different configuration and both
nodes settled fresh: each spawned child reported back its own binding's
environment value, its own binding's argument vector, and its own binding's
working directory, and the two ran as separate processes, so neither binding's
configuration reached the other. The timeout was measured in both directions on
one binary that takes twenty seconds to come up: with a two-second timeout
declared the node settled failed carrying the agent's `spawn_failed`, and with a
sixty-second timeout declared and nothing else changed the same binary spawned,
served the dispatch, and the node settled fresh.
