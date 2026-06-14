---
decision: launch-integration
status: adopted
---

# launch-integration

## Choice

The compose verb's orchestration site runs the three role runners — scheduler, supervisor, control-api — in the same pattern as the all-in-one entrypoint: start each runner in order, track each runner's stop function, select on a combined signal-or-role-failure channel, drain in reverse order. The three role runners are the same code paths the deployed `concept:supervisor`, scheduler, and `concept:control-api` binaries run. The process-role marker is set so the memory-blob backend gate (per `concept:blob-backend`) permits memory if chosen.

## Rationale

The three role runners are the natural reuse unit — the same code paths the deployed binaries run. The orchestration pattern (start / track / select / drain) is mechanical and small enough to mirror in a sibling site rather than entangle the compose verb's wiring with the entrypoint binary.
