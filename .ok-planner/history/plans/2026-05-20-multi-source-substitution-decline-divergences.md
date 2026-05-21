# Divergences — 2026-05-20-multi-source-substitution-decline

Audit of `.ok-planner/plans/2026-05-20-multi-source-substitution-decline.md` against the working tree after implementation.

## Forced adaptation

### Task 5: `mv` substituted for `git mv` (forced)

- **What the plan said:** Step 5.2 directs `git mv .ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md .ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`, and Step 5.3 expects `git status --short` to show "one `R` (rename) entry covering the two paths."
- **What was implemented:** The file now exists at `.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md` and is absent from `.ok-planner/sketches/`. `git status --short` shows the new path as an untracked file (`?? .ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`) — there is no `R` rename entry, and the old path appears nowhere in the status output. `git log --all -- .ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md` returns no commits, confirming the source was never tracked.
- **Inferred reason:** Forced choice. The plan's precondition assumed the sketch was a tracked file (citing precedent commit `cce2f1d`), but it was untracked — consistent with the sibling sketches still sitting untracked in `.ok-planner/sketches/` (`2026-05-19-multi-instance-template-ergonomics.md`, `2026-05-19-rimsky-code-review-orchestrator.md`). `git mv` would have failed with "fatal: bad source"; plain `mv` is the only way to accomplish the documented intent (move the file unchanged from `sketches/` to `history/sketches/`). The end state — file present at destination, absent at source, contents unchanged — matches what the plan asked for.

## Other divergences

None. The three edited files (`concepts/attribute.md`, `concepts/node-subscription.md`, `CHANGELOG.md`) carry exactly the text the plan specified, at the insertion points the plan specified, with the chronological ordering the plan called for. All Task 7 grep checks return the expected counts (1 each), and no Go or other source files were touched.

Note on internal cross-references: the new Notes bullet on `attribute.md` and the new CHANGELOG bullet both reference the spec at `.ok-planner/history/specs/2026-05-20-multi-source-substitution-decline-design.md`, while the spec currently still lives at `.ok-planner/specs/2026-05-20-multi-source-substitution-decline-design.md`. This is not a divergence — the plan dictates that exact text verbatim from the spec, and per `.ok-planner/CLAUDE.md` the spec gets auto-archived to `history/specs/` when the execute-plan run finishes, so the citations will resolve once archival runs.
