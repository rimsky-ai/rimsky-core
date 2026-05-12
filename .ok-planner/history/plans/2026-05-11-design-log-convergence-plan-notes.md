# Implementation notes — 2026-05-11-design-log-convergence-plan

Durable record of deviations, judgment calls, and items for post-run discussion. Subagents append entries here as they work; the orchestrator surfaces these with the user after review.

Plan: `.ok-planner/plans/2026-05-11-design-log-convergence-plan.md`
Spec: `.ok-planner/specs/2026-05-11-design-log-convergence.md`

## Task 16 — CLAUDE.md sweep

**Deviation:** No edits required.
**Reason:** Grep for `5 methods|five[- ]method|five methods|mcp-server|licensing-boundary|scenario-harness|userdata-overrides` against CLAUDE.md returned zero hits. CLAUDE.md already uses the "4 verbs + Capabilities() startup handshake" framing throughout, and does not name any of the four dropped concept slugs in load-bearing prose.
**Surfaced for:** confirmation that the CLAUDE.md sweep ran and the file genuinely needed no changes.

## Task 19 — scenario test flake observed

**Deviation:** A first run of `make test-all` failed `TestAtomicAcquisitionRollsBackOnOpenError` in `test/scenarios/locks/` with "Open error must surface" (got nil). Investigated by stashing changes and re-running on the clean tree — the test passed on the clean tree, then passed again after un-stashing (with my changes restored), then passed three times in a row on `go test ./test/scenarios/locks/ -count=3`.
**Reason:** Intermittent flake unrelated to this plan's edits. The atomic-acquisition rollback path is in `runner_acquire.go`, which was not touched. The Open-error injection through the in-memory Fake is independent of `abandonOpenedClaim` (which only runs on Abandon, not Open).
**Surfaced for:** awareness — this test may be a flaky candidate worth a follow-up flake-hunt (`-race -count=N`). Not blocking this plan.

## Task 17 — Adjacent block sweep findings

**Deviation:** All four dropped slugs (`licensing-boundary`, `mcp-server`, `scenario-harness`, `userdata-overrides`) appear only in `_discover/...md` references, directory paths (`mcp-servers/control-api/`), or historical-note prose inside their fold-destination concept files. Zero dangling `Adjacent:` slugs remain.
**Surfaced for:** confirmation that the sweep ran clean.

## Review cleanup cycle 3 — handler-slot-count-drift resolved

**Judgment call:** The reviewer's flag on Issue 6 left it to my discretion whether to move `handler-slot-count-drift.md` to `_resolved/` (the reviewer's read was that the on-event-handler promotion structurally resolves it; mine agrees). The tension's "What is muddy" was specifically about CLAUDE.md framing vs `docs/concepts/handlers.md` framing; with `on-event-handler` now a sibling concept distinct from `lifecycle-handler`'s four slots, the catalog's own framing is unambiguous (4 + sibling-concept). The CLAUDE.md vs `docs/concepts/handlers.md` drift is now narrative-prose drift that lives outside the catalog's concept boundaries; closing the catalog-side tension is the right move. Moved with shape `four-plus-on-event-handler-promoted` and a doc-sweep block citing the two concept files that absorbed the resolution.
**Surfaced for:** sign-off on the shape and the move (a future `docs/concepts/handlers.md` audit may want to follow up at the prose-level).

## Review cleanup cycle 3 — third Abandon site migration

**Judgment call:** Issue 2 asked me to migrate `handleOrphanedClaim` (the verify-before-run race-detection bail path) to use `abandonOpenedClaim` instead of `lk.Store.Abandon` directly. I chose the helper-migration path rather than routing through `ResolveClaimHandleTerminal` because the two sites have legitimately different shapes: `ResolveClaimHandleTerminal` runs inside a single caller-provided tx that pairs the producer verb with a claimant-guarded `ClaimHandles.Delete`; `handleOrphanedClaim` already commits per-claim cleanup independently (Abandon then a separate per-claim `Persist.Transaction(... ClaimHandles.Delete ...)` call) before emitting the `orphaned_claim_lost_race` event. Folding the second shape into the engine would have meant a structural rewrite of `handleOrphanedClaim` that isn't covered by this plan's spec; the helper migration preserves the same atomicity surface and gives the centralized telemetry/audit hook the reviewer's framing requires.
**Surfaced for:** sign-off on the helper migration vs. structural rewrite trade-off, and on the auto-terminal.md / terminal-resolution.md prose update that distinguishes the two carve-outs (pre-dispatch + verify-before-run bail) from the post-dispatch unified-engine path.

## Review cleanup cycle 4 — dangling Adjacent-slug sweep (Task 17 thoroughness pass)

**Deviation:** Reviewer's re-review of Task 17 ("Adjacent block scrub") flagged a remaining dangling slug — `substitution` in `concepts/attribute.md`'s Adjacent list, pointing at a nonexistent `concepts/substitution.md`. A broader sweep across all 46 concept files surfaced three more dangling slugs that the original Task 17 pass had missed:

| File | Dangling slug | Fix |
| --- | --- | --- |
| `concepts/attribute.md` | `substitution` | Dropped (Option A); the grammar is documented inline in `attribute.md`'s Owns block and `_discover/2026-05-10-attribute-substitution-grammar.md` is already cross-referenced. |
| `concepts/advisory-lock.md` | `scheduler`, `migrate` | Rewired to `schedule` (the cron-expression concept that owns the scheduler-tick loop and inherits the advisory-lock gate) and `persistence-driver` (which owns the migration runner per its boundaries). |
| `concepts/schedule.md` | `scheduler` (explicit "(the process)" parenthetical) | Replaced with `advisory-lock` (the tick gate). The "(the process)" disambiguator was a tell that no concept noun for the binary exists; it pointed nowhere. |
| `concepts/supervisor.md` | "see `scheduler`" prose reference (Does-NOT-own clause) | Replaced with "see `schedule`". |

**Option choice:** Recommended Option A across the board. None of these slugs rise to load-bearing-noun status that warrants a new concept file. Scheduler-as-a-process-binary is captured by `module-layout` ("The three runtime processes (scheduler, supervisor, control-api)" prose) plus the catalog-level visibility through `schedule` and `advisory-lock`. The `migrate` binary doesn't need a concept noun separate from `persistence-driver`'s migration-runner ownership.

**Note on the supervisor concept:** `supervisor.md` IS a concept while `scheduler.md` is not. The asymmetry is real but pre-existing: the supervisor carries far more load-bearing surface area (acquisition tx, dispatch, terminal-handler resolution, callback HTTP server, heartbeating, claimant-guarded release discipline, the verify-before-run guard) than the scheduler (which is mostly a thin advisory-lock-gated cron loop already covered by `schedule` + `advisory-lock`). Not promoting a parallel scheduler concept preserves the catalog's load-bearing-only discipline. If a future tension shows scheduler-side surface area not captured by `schedule`/`advisory-lock`/`module-layout`, that's the moment to revisit.

**Catalog count:** Still 46 concepts. No new concept files created; no entries removed; only Adjacent text rewired.

**Verification:** Re-ran the dangling-slug sweep extracting backtick slugs strictly from text after `Adjacent: ` on each Adjacent line:

```sh
rg -N 'Adjacent' .ok-planner/design/concepts/ | while IFS= read -r line; do
  adj_part="${line#*Adjacent: }"
  echo "$adj_part" | grep -oE '`[a-z][a-z0-9-]*`'
done | tr -d '`' | sort -u | while read slug; do
  test -f ".ok-planner/design/concepts/${slug}.md" || echo "DANGLING: $slug"
done
```

Output: empty. Zero dangling slugs remain across all 46 concept files.

**Surfaced for:** confirmation of the Option A choice and the asymmetry note on scheduler-vs-supervisor concept status.

