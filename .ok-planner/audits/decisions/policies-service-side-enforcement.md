---
audit: policies-service-side-enforcement
artifact: decision:policies-service-side-enforcement
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Bundled-service policy enforcement (claude-agent's allowlists) lives entirely inside the service

Supported. `lib/services/executors/claude-agent` is the one bundled service carrying operator allowlists (`RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST`, `RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST`, read in `opts.go`), and `lib/services/executors/claude-agent/agentrun.go` enforces both entirely inside the handler: a disallowed `expose_env` entry or MCP server name returns an `agent/attribute_invalid` executor-protocol error outcome whose payload names the specific disallowed entry (`disallowed_env_var` / `disallowed_mcp_server`) plus `instance_id` and `node_id`, before any spawn happens. `lib/services/executors/claude-agent/agentrun_test.go::TestRunAgentExposeEnvAllowlistViolation` and `TestRunAgentMcpAllowlistViolation` both assert this shape directly. A repo-wide grep for "allowlist" outside `lib/services/` turns up no reference to these (or any comparable service-policy) allowlist in `lib/control`, `lib/runtime`, `lib/graph`, or the proto definitions — `executor.proto`'s `ExecuteRequest` carries no policy field — confirming rimsky's dispatch payload and protocol are inert to this policy content, consistent with the decision's rejected alternative (a rimsky-side pre-filter).
