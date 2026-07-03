---
decision: cli-spawn-mechanism
status: as-is
aliases: []
---

# claude-agent spawns the agent CLI via the standard subprocess mechanism

## Choice

The claude-agent handler shells to the `claude` binary via the Go standard-library subprocess mechanism, with argument and env composition mirroring the retired TypeScript implementation's behavior (print/stream-json output, per-run session id, system-prompt and MCP-config temp files, allowed-tools union over the callback surface).

## Rationale

The retired TypeScript version was itself a subprocess spawner around the same CLI; the Go port replaces the Node runtime with the standard-library primitive and drops one process layer. The CLI owns the session protocol, tool loop, and provider surface; the handler owns dispatch lifecycle, callbacks, and teardown.

## Alternatives

Reimplement the agent's session protocol against the raw HTTP API — rejected: reinvents wheels the CLI already handles and diverges from the CLI's evolving surface.
