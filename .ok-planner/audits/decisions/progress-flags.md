---
audit: progress-flags
artifact: decision:progress-flags
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:37Z
---

# Three composable progress flags: quiet, verbose, json

Supported. `cmd/rimsky/cli/compose/progress.go` defines four `ProgressPrinter` implementations dispatched by `newProgressPrinter(w, quiet, verbose, jsonMode)`: `quietPrinter` suppresses all per-event lines (only `Summary` remains active), `verbosePrinter` adds a `FrameTick` line on top of the default instance/node/instance lines, and `jsonPrinter` renders every event — including `Summary` — as one JSON object per line via `json.Marshal`. `--quiet` and `--verbose` are enforced mutually exclusive in `parseComposeRunFlags`, while `--json` composes with `--quiet` (checked by `newProgressPrinter`'s `if jsonMode` short-circuit ahead of the quiet/verbose checks — quiet's per-event no-ops still apply, giving the summary-only structured shape the decision names). `progress_test.go` covers all four printers (`TestQuietPrinter_SuppressesEvents`, `TestVerbosePrinter_EmitsFrameTicks`, `TestJSONPrinter_EmitsJSONLines`, `TestJSONPrinter_SummaryEmitsParseableJSONLine`) plus the flag-to-printer dispatch (`TestNewProgressPrinter_FlagDispatch`).
