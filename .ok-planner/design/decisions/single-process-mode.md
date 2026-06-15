---
decision: single-process-mode
status: as-is
---

# The all-in-one runs all three roles in one process

## Choice

The entrypoint's no-command path runs migrate synchronously, then starts all three roles (scheduler, supervisor, control-api) in-process via the existing library entry points, each on its configured port, with one signal-handled shutdown. The single-role path (explicit role command) keeps its per-role process behavior. A process-role env marker that names the unified single-process mode is set only in — and truthfully describes — that mode (see `concept:replica`, `story:single-process-all-in-one`).

## Rationale

The unified env marker and the memory-blob gate both promise a shared-process deployment; the role mains are thin wrappers over library calls, so the promised deployment is the cheap honest fix.

## Alternatives

Keep three spawned processes and remove the memory gate (rejected: leaves the unified marker meaningless and the memory backend useless even in dev).
