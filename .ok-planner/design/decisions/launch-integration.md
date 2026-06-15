---
decision: launch-integration
status: adopted
---

# launch-integration

## Choice

The compose verb's orchestration site runs the three role runners — scheduler, supervisor, control-api — in the same pattern as the all-in-one entrypoint: start each runner in order, track each runner's stop function, select on a combined signal-or-role-failure channel, drain in reverse order. The three role runners use the same single-process all-in-one launcher the deployed unified-stack entrypoint uses (see `concept:supervisor`, `concept:control-api`). The process-role marker is set so the memory-blob backend gate (per `concept:blob-backend`) permits memory if chosen.

## Rationale

The three role runners are the natural reuse unit — the same launcher the deployed unified-stack entrypoint uses. The orchestration pattern (start / track / select / drain) is mechanical and small enough to mirror in a sibling launch site rather than entangle the compose verb's wiring with the entrypoint shape.
