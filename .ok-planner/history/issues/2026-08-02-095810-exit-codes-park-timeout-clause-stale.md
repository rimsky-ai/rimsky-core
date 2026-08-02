---
issue: exit-codes-park-timeout-clause-stale
kind: audit
category: doc-drift
artifacts:
  - decision:exit-codes
status: repaired
opened: 2026-08-02T09:58:10Z
---

# Does exit code 1 still cover a park-timeout failure, and does `concept:auto-terminal` carry run-timeout/park-timeout semantics?

No to both. Park-timeout was retired by migration 025 (`TestNextState_ParkTimeoutRetired` proves the state machine now rejects a `park_timeout` transition reason), so no run can ever reach exit code 1 via park-timeout. `concept:auto-terminal` never mentioned run-timeout or park-timeout at all — the cross-reference was broken independent of the retirement. The run-timeout half (exit code 2) is real and lives in the CLI's run-to-terminal machinery (`decision:timeout-flag`, realized in `cmd/rimsky/cli/compose/wait.go`/`shutdown.go`), not in `concept:auto-terminal`. The four-way exit-code scheme (0/1/2/130) itself is correctly implemented and untouched by this repair.

Rule that determined the fix: outcome-2 corpus-side repair — a retired feature's leftover clause plus a broken cross-reference, no commitment change (the exit-code scheme is unchanged).

Changed: `.ok-planner/design/decisions/exit-codes.md` — dropped the "(including park-timeout as a failure)" parenthetical and repointed the run-timeout cross-reference from `concept:auto-terminal` to `decision:timeout-flag`.
