---
decision: exit-codes
status: adopted
---

# exit-codes

## Choice

`0` for all-instances-success; `1` for at-least-one-failure (including `park_timeout`); `2` for `--timeout` exceeded; `130` for SIGINT during shutdown.

## Rationale

Three distinguishable classes for script-friendly branching. `130` is the conventional shell-signaled-exit code for SIGINT (signal number 2 + 128).
