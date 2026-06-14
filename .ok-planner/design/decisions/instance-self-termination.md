---
decision: instance-self-termination
status: adopted
---

# instance-self-termination

## Choice

Every instance the verb creates is created with self-termination enabled — the same `terminate_after_run` knob that `rimsky run --no-keep` already sets. The compose manifest's `InstanceRef` does not need to carry a per-instance flag; `compose run` hardcodes the value.

## Rationale

Per `concept:instance`, an instance is durable by default and self-terminates only when created with self-termination on. Without this, the existing compose-apply path creates durable instances that never reach terminal — the verb's terminal-wait loop would spin until `--timeout` regardless of whether the actual work completed. Hardcoding the flag matches the user intent of `compose run` (a one-shot, not a deployment) and respects the existing instance invariant.
