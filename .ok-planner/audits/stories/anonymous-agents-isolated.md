---
audit: anonymous-agents-isolated
artifact: story:anonymous-agents-isolated
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:16Z
---

# Concurrent anonymous host-agents each receive only their own instances' dispatches

Supported. The host-agent-proxy keys its routing table by an assigned per-agent silly-name (`lib/foundation/sillyname`), rejects a registration whose routing label collides with a currently-connected anonymous agent (`AlreadyExists`, verified in `cmd/rimsky-host-agent-proxy/agent_server_test.go` and `register_auth_test.go`) rather than displacing it, and an instance created against a given anonymous agent is stamped with that agent's routing identity so dispatches resolve only to it. The end-to-end scenario `TestHostAgentAnonymousModeMultiAgentIsolation` (`test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go`) runs two concurrently-connected anonymous agents, creates one instance targeting each, confirms a colliding routing-label registration is rejected while the live agent stays connected, and asserts each instance's worker dispatched exactly once and only through its own agent's spawned process (checked via a per-agent exec log and per-node run count) — plus a reconnect-after-disconnect case showing the freed routing identity is usable again.
