---
decision: timeout-flag
status: adopted
---

# timeout-flag

## Choice

`--timeout <duration>` is opt-in. No default.

## Rationale

A default wall-clock cap kills legitimate long-running runs. Operators who want a guard add the flag; absence means "as long as it takes."
