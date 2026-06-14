---
decision: progress-flags
status: adopted
---

# progress-flags

## Choice

`--quiet` collapses output to the final aggregate summary; `--verbose` adds frame ticks, scheduler decisions, and claim-handle events; `--json` switches every line to a JSON object (JSON Lines format).

## Rationale

Three operating modes cover CI logs (quiet), live debugging (verbose), and structured pipelines (json). They compose: `--quiet --json` is the structured CI shape.
