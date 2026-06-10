---
concept: host-agent
status: as-is
aliases: []
---

# Host agent

## What it is

A long-running daemon on a user's dev machine, bundled into the `rimsky` CLI binary and invoked as the `rimsky agent` subcommand. Authenticates outbound to a `concept:host-agent-proxy` with the user's `concept:api-key`. Serves spawn / dispatch / reap / local-HTTP-forward requests against locally-running binaries.

## Purpose

Lets users run arbitrary local binaries as rimsky services on a per-invocation basis without static deployment configuration. Eliminates the manual "start the local process, wire up reachability, trigger an instance, tear down on completion" setup that would otherwise be required for dev workflows.

## Boundaries

Owns: dev-machine process spawn/exec, local HTTP listener termination, the agent-side end of the agent ↔ proxy bidi stream, child-process reaping on Reap or connection close. Does NOT own: service discovery, capability advertisement (the spawned binary advertises its own Capabilities via the proxy-driven handshake), persistent state across restarts, the supervisor-facing service protocols (those live on the proxy). Adjacent: `concept:host-agent-proxy`, `concept:service`, `concept:api-key`.

## Invariants

- No capability config; the agent does not know in advance what binaries exist.
- Path resolution happens at exec time; absolute, relative, and bare-name paths all work via the shell search path.
- Spawned children inherit the agent's full environment.
- On bidi-stream close (clean or unclean), all live children are sent a terminate signal and force-killed after a configurable grace period.
- The agent has no persistent state of its own; it reads auth from the CLI's active-context config (the existing user config file, extended with an api-key field).
