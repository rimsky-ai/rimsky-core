---
decision: operator-env-namespaced-per-service
status: as-is
aliases: []
---

# New bundled-service operator env vars are namespaced per service

## Choice

Operator env vars introduced for a bundled service carry a per-service prefix of the form RIMSKY_<SERVICE>_*. For claude-agent these are RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST (replacing the retired catalog-file and inline-allow envs) and RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST (renaming and reshaping the retired container-wide exposure env). Pre-existing generic per-executor env vars (host, ports, CLI binary override, declared tags, timeouts, stub mode) survive unchanged — they are relevant only to standalone deployment; an in-process handler binds no ports and reads no transport envs, so cross-service collision does not materialize there.

## Rationale

In a unified process the handler shares its environment with rimsky-core's own env vars and other bundled handlers'. Namespacing prevents collision on the new operator envs and makes provenance obvious.

## Alternatives

Non-namespaced names — rejected: risks collision with unrelated env in the host process; makes provenance unclear.
