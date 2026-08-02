---
decision: progress-flags
---

# Progress output has three composable flags

## Choice

`--quiet` collapses output to the final aggregate summary; `--verbose` adds frame-tick lines on top of the default instance-starting / node-run-terminal / instance-terminal lines; `--json` switches every line to a JSON object (JSON Lines format).

## Rationale

Three operating modes cover CI logs (quiet), live debugging (verbose), and structured pipelines (json). They compose: `--quiet --json` is the structured CI shape.

## Alternatives

- A single numeric verbosity level — rejected: format (JSON) and volume (quiet/verbose) are orthogonal axes that must compose.
