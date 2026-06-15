---
decision: instance-self-termination
status: adopted
---

# instance-self-termination

## Choice

Every instance the compose-run verb creates is created with self-termination enabled — the same terminate-after-run knob that the standalone run verb's transient mode sets. The compose manifest's instance reference does not need to carry a per-instance flag; the compose-run verb hardcodes the value.

## Rationale

Per `concept:instance`, an instance is durable by default and self-terminates only when created with self-termination on. Without this, the existing compose-apply path creates durable instances that never reach terminal — the verb's terminal-wait loop would spin until the run-timeout regardless of whether the actual work completed. Hardcoding the flag matches the user intent of the compose-run verb (a one-shot, not a deployment) and respects the existing instance invariant.
