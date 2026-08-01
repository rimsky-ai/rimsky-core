---
story: single-process-all-in-one
status: as-is
---

# Operator runs the all-in-one deployment as one process

## Role

As an operator running the all-in-one deployment, I get one process serving all three roles (scheduler, supervisor, control-api), so that the deployment is genuinely unified — including the memory blob backend working there, because the roles actually share a process.

## Capability

The all-in-one entrypoint's no-command path runs migrations synchronously, registers the bundled executor and claim-producer handlers in-process (see `decision:bundled-registry-entrypoint`), and then starts all three roles in-process, each on its configured port, with one signal-handled shutdown. A process-role env marker that names the unified single-process mode is set only in — and truthfully describes — this mode, which is what gates the in-memory blob backend (see `decision:single-process-mode`, `decision:memory-gate-premise-corrected`, `concept:blob-backend`, `concept:replica`). Single-role deployments (an explicit role command per container) keep their per-role process behavior.

## Business value

The unified deployment marker and the memory blob backend's gate both describe a deployment that actually exists: one process, genuinely shared in-memory state, an orphan-blob sweep that reaps the map the roles actually write to.

