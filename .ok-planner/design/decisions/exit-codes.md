---
decision: exit-codes
status: adopted
---

# Exit-code classes

## Choice

Zero for all-instances-success; one for at-least-one-failure (including park-timeout as a failure); two for the run-timeout exceeded; the conventional interrupt-signal exit code (signal number 2 plus 128) for interrupt during shutdown. See `concept:auto-terminal` for the run-timeout and park-timeout semantics.

## Rationale

Three distinguishable classes for script-friendly branching, plus the conventional shell-signaled-exit code for interrupt.

## Alternatives

- A single nonzero failure code — rejected: scripts cannot branch run-timeout expiry apart from instance failure.
- A distinct code per failure kind — rejected: proliferates codes beyond what shell branching uses; the run's own output carries the detail.
