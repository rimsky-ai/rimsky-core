---
sketch: last-mile-stability
date: 2026-06-10
---

# Last mile to stability: race-honest harness first, then consolidate the duplicated paths

Kickoff sketch for a "last mile" brainstorm. The thesis: rimsky's
remaining instability is not scattered bugs — it is a small number of
places where the design allows **two code paths to the same outcome**,
with parity between them maintained by hand. Several of these are
already documented as open tensions; the races we keep fighting are the
carrying cost of leaving them unresolved. The proposal is a sequenced
campaign: make the test harness able to *see* races first, then
consolidate the duplicated paths one tension at a time, folding
error-surface ergonomics into the code we already have open.

## Why sequence matters

The trap is starting the seam refactors first. The scenario suite
(213 files under `test/scenarios/`) verifies outcomes by
deadline-bounded polling; nothing in the default `make test-all` gate
runs under the race detector; repetition (`-count=3`) on the
race-sensitive packages exists only as a manual instruction in
`.claude/rules/rules.md`. That harness is blind to exactly the failure
class the consolidation work risks introducing. Refactoring a
concurrency seam against a race-blind harness is how a stabilization
pass produces new races — which is plausibly the loop the project has
been in across its repeated "bring the features home" passes.

So: buy the instruments before flying into the storm.

## Phase A — race-honest harness

Goal: every subsequent seam change is verifiable, not hopeful.

- Wire `-race` into the default test gate for the race-sensitive
  packages (`lib/foundation/persistence/postgres`, `lib/runtime`,
  `lib/graph/scheduler`, queue paths), with `-count` repetition, as a
  Makefile target the release chain runs — not a manual rule.
- Extend the deterministic race-injection pattern that already exists
  for blessed-invariant 5 (`lib/runtime/runner.go` carries a hook that
  exists solely so an integration test can inject the verify-before-run
  race) to the other seam carve-outs: the acquire-unavailable abandon
  path, the verify-before-run bail's claimant-guarded delete, the
  held-claim aggregate check-and-fire, and the orphan-reaper vs
  in-flight-terminal overlap. Deterministic injection beats
  probabilistic `-race` luck for these.
- Audit the 133 test files using sleep/deadline polling: where a test
  is *waiting for a state the runtime could notify*, prefer a hook or
  event-tail wait over wall-clock polling. (Not a blanket rewrite —
  deadline-bounded waits are fine where they are genuinely
  outcome-waits; the audit is for the subset where polling masks an
  ordering assumption.)
- Sibling sketch `2026-06-10-subscribe-contention-hazard.md` documents
  a contention-driven silent failure that the `-parallel 4` Makefile
  cap papers over; Phase A should decide whether that cap stays with a
  comment, or the underlying Subscribe handshake gets the async fix,
  rather than leaving the cap as undocumented decay risk.

## Phase B — consolidate duplicated paths, ordered by blast radius

Each item rides the spec pipeline as its own tension resolution; each
lands only after Phase A gives it a harness that can falsify it.

1. **Claimant-guard chokepoint (blessed-invariant 4).** The
   `WHERE claimed_by = $supervisor` / `WHERE holder_supervisor_id = $1`
   guard predicates are hand-written across 5+ files
   (`runner_acquire.go`, `runner_terminal_release.go`, the orphan
   reaper, abandon-claim, conductor) and duplicated across the
   postgres and sqlite drivers. Centralize behind a small set of
   persistence-layer calls so the invariant has one chokepoint per
   driver instead of a string-matching discipline. Most mechanical
   item; highest leverage per unit of risk.
2. **Fold the two carve-outs into the unified claim-handle resolution
   engine — or make their exclusion load-bearing and tested.** The
   acquire-unavailable handler and the verify-before-run bail both
   Abandon claims outside the single audited verb-then-delete site
   that `concept:terminal-resolution` promises (the carve-outs are
   documented in that concept; `tension:reaper-vs-bail-abandon-asymmetry`
   is the open wound). Either the engine grows a source-kind that
   covers them, or each carve-out gets its own deterministic-injection
   test proving its divergent behavior is intended.
3. **Delegation / fan-out unification.**
   `tension:delegation-and-fanout-share-runtime-primitive`: two
   parallel implementations of the same RunScope-tree settlement
   logic, where fixes land in one path and not the other. Largest
   reshape; goes last, with the Phase A harness plus the existing
   run-tree scenario suite as the net. (The prior sketch
   `2026-05-23-unify-child-execution-sketch.md` covered ground here —
   the brainstorm should consult it.)
4. **Scheduler-tick lock error path.** Today a lock *error* (as
   opposed to lock-held) falls through to an unlocked tick, so a flaky
   DB connection can double-tick across replicas. Decide: skip the
   tick on error, or prove double-tick is harmless (every sweep
   idempotent + claimant-guarded) and document that as the invariant.
   Small, self-contained.
5. **Postgres/sqlite parity (~10k LOC each, parity by discipline).**
   Not proposing a query-builder rewrite. Proposal: extend the
   cross-driver conformance harness under
   `lib/foundation/persistence/conformance/` so every queue/claim/frame
   behavior the runtime depends on has a parity test, making drift
   (e.g. `tension:sqlite-vs-memory-reject-asymmetry`) mechanically
   detectable rather than review-detectable.

## Phase C — error-surface ergonomics, folded in opportunistically

These are core-side consumer-experience items that the docs corpus
cannot fix, because an error message is the one doc an agent always
reads. Each touches code Phase B already has open, so they fold in
rather than forming their own campaign:

- Store/producer errors collapsing to bare HTTP 500 — carry the
  producer's error class through to the control-api response.
- Template-validation failures that don't name the active
  ref-validation mode or how to change it.
- The dropped v0 sensor-observation route returning a bare 404 — a
  one-line "this route moved to POST /instances/{id}/messages" body
  costs nothing pre-v1 and saves an upgrading operator an afternoon
  of confusion. (Pre-v1 break-freely applies to *behavior*, not to
  the courtesy of saying what changed.)

## What this sketch is not

- Not a feature campaign. Nothing here adds capability; everything
  narrows the gap between what the design corpus promises and what
  the runtime provably does.
- Not a docs effort — the docs-side last mile is sketched separately
  in rimsky-docs (`sketches/2026-06-10-last-mile-docs.md` there);
  the two are independent and parallelizable.

## Open questions for the brainstorm

- Does Phase A's `-race`/`-count` gate live in `make test-all` (every
  run, slower) or as a `make test-race` stage the release chain
  requires? What does that do to the testcontainers wall-clock story?
- For the claimant-guard chokepoint: is the right shape a guarded
  persistence method (`DeleteClaimHandleClaimedBy(...)`) per
  operation, or a guard-clause builder both drivers share?
- Carve-out folding (Phase B item 2): does the unified engine grow
  `source: acquire_unavailable | verify_bail` kinds, or do the
  carve-outs stay with their own audited sites + injection tests?
  The acquire-unavailable case is structurally different (its tx
  already rolled back; there is no handle row to delete) — folding
  may be wrong there.
- Delegation/fan-out unification: full primitive merge, or a shared
  settlement library both paths call? The tension file and the
  2026-05-23 sketch frame the options.
- Is there appetite to resolve `tension:event-vocabulary-implies-delivery`
  (nomenclature) inside this campaign, or does it stay separate (the
  2026-05-29 reactive-nomenclature-rework sketch exists)? Touching
  names mid-consolidation churns every diff; probably separate, but
  the brainstorm should say so explicitly.
