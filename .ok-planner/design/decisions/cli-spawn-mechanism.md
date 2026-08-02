---
decision: cli-spawn-mechanism
---

# claude-agent spawns the agent CLI via the standard subprocess mechanism

## Choice

The claude-agent handler shells to the `claude` binary as a direct child via the Go standard-library subprocess mechanism, composing arguments and environment per dispatch, with no intermediate runtime layer.

## Rationale

The CLI owns the session protocol, tool loop, and provider surface; the handler owns dispatch lifecycle, callbacks, and teardown. Spawning the CLI as a direct child of the handler process keeps that split with the fewest process layers.

## Alternatives

- Reimplement the agent's session protocol against the raw HTTP API — rejected: reinvents wheels the CLI already handles and diverges from the CLI's evolving surface.
- Drive the CLI through an embedding agent-SDK runtime on a separate language runtime — rejected: an extra process layer and runtime dependency for the same subprocess job.
