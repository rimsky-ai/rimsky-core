---
audit: host-agent-anonymous-mode
artifact: story:host-agent-anonymous-mode
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:12Z
---

# Late-bind dispatch works end to end against an anonymous-mode agent

Supported. `test/scenarios/host_agent_anonymous_mode_latebind_test.go` boots a fixture in anonymous mode (no admin api-key minted for the agent), starts a real host-agent that registers under an explicit routing label instead of an api-key, deploys a late-bind template, creates an instance whose target routing identity is stamped to that label, and drives the worker node to `terminal/success` through a real spawned child — proving both the anonymous registration path and that an instance can target a specific connected agent by routing identity. The fixture and proxy wiring (`test/scenarios/host_agent_harness_test.go`) confirm the routing-identity targeting is exercised as a real end-to-end path, not stubbed.
