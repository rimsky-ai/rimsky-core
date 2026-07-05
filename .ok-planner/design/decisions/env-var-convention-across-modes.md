---
decision: env-var-convention-across-modes
status: as-is
aliases: []
---

# Bundled handlers read the same operator env vars in both modes

## Choice

Each bundled service handler reads its own operator config from the process's env — same env-var names in containerized mode (set on the service container) and all-in-one mode (set on the all-in-one process). Rimsky.yml carries no per-service config content in either mode. `rimsky run`'s ephemeral-run env plumbing (per `code:cmd/rimsky/cli/compose/run.go::snapshotAndSetEnv`) MUST NOT clear or reset operator env vars destined for bundled handlers — those pass through the ephemeral wrapper unchanged.

## Rationale

One convention across modes; zero rimsky.yml schema change. Templates and operator config are the same shape whether the deployment is a single all-in-one process or a multi-container stack.

## Alternatives

- Add an opaque `config:` path field per rimsky.yml entry — rejected: introduces a schema field for a case env vars already handle.
