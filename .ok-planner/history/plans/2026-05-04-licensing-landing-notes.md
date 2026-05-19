# Licensing landing — execution notes (2026-05-04)

Landed Scope A of the licensing design at
`docs/history/2026-05-02-licensing-design.md`. Single dispatch.

## Boundary refactor

The design's §4 mapping was written pre-layer-crystallization; the user
provided an updated boundary map for the post-Phase-7 layout. One new
import-graph violation surfaced during verification:

- `modeling/node/template.go` (Apache, declares `TemplateNodeDef`) imports
  `modeling/qualityrule.Spec` for the `quality_rules:` template field.
- `modeling/qualityrule/` was AGPL in the boundary map (it carried the
  registry and runtime evaluators alongside the spec types).

Resolution: split the package, not reclassify it.
- `modeling/qualityrule/` keeps the public types (`Spec`, `Failure`,
  `EvalInput`, `Evaluator` interface) — interface-shaped, classified
  Apache.
- `modeling/qualityrule/eval/` is new — carries `Register`, `Get`,
  `EvaluateAll`, and the three built-in evaluators
  (`row_count_ratio`, `no_nulls`, `nullable_fields_present`). Classified
  AGPL.
- `foundation/integration/runner_terminal.go` (the only non-modeling
  consumer) now imports both packages: types from `qualityrule`, the
  `EvaluateAll` runtime from `qualityrule/eval`.

This split is consistent with the design doc's principle (§4.3) of
classifying interface-shaped types Apache and orchestration runtime AGPL.

## Stamper details worth knowing

Two header-stamping decisions worth recording:

1. **Proto files: header at the very top, not under `syntax`.** The
   spec text said "prepend AFTER the `syntax = "proto3";` line"; in
   practice proto3 accepts leading comments before `syntax` (every
   non-trivial proto file in the wild has them — Google, Apache, etc.).
   Putting the header on top simplifies the stamper and ensures the
   verify-scan window (10 lines) finds the marker regardless of
   doc-comment length above the header.
2. **TS shebang preservation.** `executors/claude-agent/src/main.ts`
   carries a `#!/usr/bin/env node` shebang. The stamper's `spliceTS`
   detects it and keeps it on top, header below — same pattern as the
   `// Code generated` handling for Go.

## Verification ran

- `go build ./...` (root + foundation + protocols): clean.
- `go test ./... -count=1` (root + foundation + protocols): clean.
- `make lint`: clean after gofmt + ineffassign fixes in the
  license-check binary.
- `make license-lint`: 252 apache files, 227 agpl files, 0 violations.
- `cd executors/claude-agent && npm test && npm run build`: clean.
- Spot-checked headers on foundation/cascade, foundation/locks,
  protocols/claimproducer, modeling/scheduler, modeling/cli — all
  match expected classification.

## Scope deferred

- **CLA bot configuration.** Off-repo (cla-assistant.io GitHub
  integration); the `CLA.md` text and `CONTRIBUTING.md` pointer landed,
  but the bot itself is wired by an operator action.
- **DCO bot configuration.** Same — `probot/dco` is configured at the
  GitHub-app level.
- **CI workflow.** Repo has no `.github/workflows/`. The `make
  license-lint` target is in place; wiring it into a CI workflow file
  is a follow-up when the workflow infrastructure lands.
- **USPTO trademark filing.** Per design §10, deferred indefinitely.
- **Commercial license contract.** Per design §9.7, drafted on first
  prospect.
- **`licensing@fallguyconsulting.com` mailbox.** Off-repo operational
  task.
