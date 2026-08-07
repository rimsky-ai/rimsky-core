---
issue: compose-demo-scripts-assume-full-checkout
kind: human
category: developer-experience
artifacts: []
status: repaired
opened: 2026-08-06T08:07:42Z
github: https://github.com/rimsky-ai/rimsky-core/issues/62
---

# Do examples/compose/*-demo.sh assume a full checkout and break when the examples module is vendored?

Yes, confirmed on the current tree: all three scripts
(`one-shot-to-terminal-demo.sh`, `live-progress-demo.sh`,
`audit-artifact-demo.sh`) defaulted `RIMSKY_BIN` to a build step —
`(cd "$HERE/../.." && go build -o "$RIMSKY_BIN" ./cmd/rimsky)` — reaching
two directories above `examples/compose/` into `cmd/rimsky`, which does not
exist in a vendored copy of just `examples/` (rimsky-docs ships one).

The rules determine the fix without new judgment: every other
`examples/*-demo.sh` script in the tree (`onboarding-demo.sh`,
`host-agent-control-plane-demo.sh`, `client-context-demo.sh`) already uses
one uniform idiom — `RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"`, resolving the CLI
from `PATH` with no build fallback and no full-checkout assumption. Per
Plumbline's Uniformity rule ("one idiom per job, repo-wide … sweep the old
one out everywhere in the same change"), the three compose scripts were the
outliers, not a second legitimate idiom. No production/CI caller invokes
these scripts non-interactively (grepped the whole tree; only
`.ok-planner/history/` planning docs reference them by path), so nothing
depended on the build-fallback behavior.

**Change:** all three scripts — replaced the build-fallback block with
`RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"` plus a `command -v` existence check
that fails loud naming `RIMSKY_BIN` and suggesting `go build -o
/path/to/rimsky ./cmd/rimsky` as the override, matching the sibling
scripts' resolution idiom while keeping (rather than silently dropping) a
clear error for the not-found case.

**Verified:** `bash -n` on all three scripts; ran all three end-to-end with
`RIMSKY_BIN` pointed at a freshly built `./cmd/rimsky` binary — all three
`PASS` (exit 0) reproducing their existing assertions
(mixed-outcome/live-progress-timing/audit-artifact-query legs unchanged);
ran `one-shot-to-terminal-demo.sh` with `RIMSKY_BIN` unset against `PATH`
and separately with a nonexistent `RIMSKY_BIN` path to confirm the
not-found error message fires correctly.
