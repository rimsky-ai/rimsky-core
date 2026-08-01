---
issue: conflict-process-role-marker-setter-count
kind: audit
category: conflicting
artifacts:
  - decision:single-process-mode
  - decision:process-role-unified-message-covers-rimsky-run
status: repaired
opened: 2026-07-24T00:00:00Z
---

# Do the corpus's process-role-marker decisions and story agree with the four-setter code?

`decision:single-process-mode` already states the marker has four setters (the three genuine deployment paths plus the conformance runner's test-only setter) and already frames the three as "the three the marker's blob-config error text names" — matching the code (`lib/foundation/persistence/blob_config.go`'s error text names three paths; `cmd/rimsky/conformance_blob_backend.go` sets the marker as an unnamed fourth). The residual conflict was narrower than the original filing: `decision:process-role-unified-message-covers-rimsky-run`'s Choice read "names all three paths that set the marker," which overclaims exhaustiveness against the four-setter fact `decision:single-process-mode` and the code already establish, and `story:single-process-all-in-one` claimed the marker "is set only in ... this mode," which the conformance runner's setter also contradicts.

The rules determine the fix and it changes no commitment: the four-setter fact, and the error text's deliberate three-path scope, were already the corpus's and code's agreed commitment — only two sentences' wording overclaimed against it. Repaired by aligning both sentences to the commitment `decision:single-process-mode` and the code already share, per the mechanical-vs-judgment rule (a wording correction that changes no commitment, not a merge/retirement of either decision).

Changed:
- `.ok-planner/design/decisions/process-role-unified-message-covers-rimsky-run.md` — Choice now reads "names the three genuine deployment paths" (not "all three paths that set the marker"), with a sentence naming the fourth, deliberately-unnamed conformance-only setter and a cross-reference to `decision:single-process-mode`.
- `.ok-planner/design/stories/single-process-all-in-one.md` — the marker clause now reads "is set by this mode's own startup path, and truthfully describes shared process state for every genuine deployment path that sets it," dropping the false "only" claim, with a cross-reference to `decision:single-process-mode`.

Verified against code: `grep -rn ProcessRoleEnv` confirms exactly four setters (`cmd/rimsky-entrypoint/main.go`, `cmd/rimsky/cli/compose/run.go`, `cmd/rimsky/cli/compose/template_run.go`, `cmd/rimsky/conformance_blob_backend.go`), and the error text in `lib/foundation/persistence/blob_config.go` (annotated `@decision: process-role-unified-message-covers-rimsky-run`) names exactly three. Docs-only change; no build/test impact.
