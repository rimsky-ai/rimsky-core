---
decision: launch-integration
---

# The compose verb and the entrypoint share one role launcher

## Choice

One exported launcher runs the three role runners — scheduler, supervisor, control-api: it starts each in order, tracks each runner's stop function, owns the combined role-failure channel, and drains in reverse order. Both the all-in-one entrypoint and the compose verb call it. Each site writes its own signal-versus-failure select, because each has its own signal source. The process-role marker is set so the memory-blob backend gate (per `concept:blob-backend`) permits memory if chosen.

## Rationale

The start / track / fail / drain loop is identical at both sites and load-bearing at both — a drain that runs in the wrong order or a failure channel nobody owns is a shutdown bug, not a style difference — so it lives in one place and the two sites cannot drift. What genuinely differs is the signal source: the entrypoint watches process signals, the compose verb watches its own lifecycle, so the select stays per site.

## Alternatives

- Mirror the loop at both sites rather than share it — rejected: two copies of a shutdown ordering that must agree, with nothing to keep them agreeing.
- Spawn the all-in-one entrypoint as a child process from the compose verb — rejected: forfeits in-process control of the runners (config injection, lifecycle, teardown) that the verb needs.
