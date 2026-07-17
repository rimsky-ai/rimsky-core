# Drift-Remediation Execution Plan

Adjudicate and remediate the review findings against the design-intent ledger
(`.ok-planner/design/intent/`), without collapsing features that drift already
weakened and without resurrecting features that were deliberately retired.

This document is the **operating doc** for the effort and is written to be
**resumable from a clean context**: a fresh session should be able to read this
top-to-bottom and continue without prior conversation history.

## Status (as of 2026-07-17)

**Current `dev` tip: `408eb3c5`.** Since this plan's Track 0b commits, the
separate security track ran to completion and landed 8 commits on `dev`
(`5f8c10f1` through `408eb3c5`). Those belong to the security track, not this
one — do NOT read the commit SHAs listed below as the branch head; reconcile
against `git log` starting from `408eb3c5`. This track's own progress is
unchanged: Phase 1 (design-corpus reconciliation) is still the next step.

**Track 0a — conflict rulings: DONE.** All 33 distinct dossier conflicts
resolved by user ruling, written back into the dossiers. Committed `993cd3ad`.

**Track 0b — intent-independent critical defects: DONE and gate-verified.**
Fixed, each with a regression test and full verification; `make test-all` green
end to end (all five modules incl. docker-stack services + examples, zero
timeout panics). Commits:
- `233d567c` batch 1 (8 criticals) · `74eeb36b` batch 2 (6 criticals + test
  determinism) · `e03aafda` batch 3 (3 criticals + a design ruling)
- `0180d790` staged_async external-cross-reference data-loss fix (a regression
  that stale-image verification had masked — see Environment discipline below)
- `e7d4eacc` build test images once (ended the Docker-daemon wedging)
- `72e57828` proxy-startup wait made deterministic
- `6fd4ace3` · `01dc2906` · `58e5a7b8` follow-up tracking + workbench folder

**Next: Phase 1** (design-corpus reconciliation), ungated for every concept now
that Track 0a is complete.

## Ledgers (two, separate tracks)

- **Main ledger** — `review-findings-2026-07-06.csv`: **2,325 rows** (1,114
  CONFIRMED, 62 REFUTED, 2 DUPLICATE, 1,147 unverified). REFUTED and duplicates
  are closed/skipped; unverified rows get verified as a side effect of
  adjudication. This is the Phase 1–3 work.
- **Security ledger** — `review-findings-security-2026-07-06.csv` (40 rows): a
  **separate track**, NOT part of the Phase 1–3 fleet. Worked on the
  security-cleared model per its own doc (`2026-07-15-security-remediation.md`).
  Do not pull it into this general-remediation context. **COMPLETE as of
  2026-07-17** (36 fixed / 4 accepted on the ledger; pre-commit review 12 fixed /
  2 accepted), landed on `dev` through `408eb3c5`. Nothing left to do there;
  it stays out of this context.

## Environment & verification discipline (hard-won — do not relearn)

- **`make test-all` is the real gate.** It builds `core-images` +
  `service-images` + `test-images` FIRST, then runs every module bounded. Earlier
  batches were verified with `cd lib/services && go test ./...` against **stale
  images** and passed *vacuously* — that masked a committed data-loss regression
  (`0180d790`) until the first fresh-image run. Any change touching core/service
  code must go through `make test-all` (or rebuild the specific image) before it
  is considered verified.
- **Read real exit codes from files, not the wrapper.** A `( go test …; echo $? >f )`
  subshell's completion status is NOT the test exit; read the captured `$?` file
  and grep the log for `--- FAIL` / `panic: test timed out`. The background-task
  "exit code 0" notification is the wrapper, not go test.
- **Distinguish hang from failure.** `panic: test timed out after 10m0s` on a
  whole package = a hang (resource starvation / harness), not an assertion
  failure. Docker resource exhaustion (accumulated testcontainers) manifests as
  every heavy package timing out at ~600s. Recovery: prune testcontainers debris
  (`docker container/image/volume/network prune`), rebuild images, run suites
  serially with bounded parallelism. The test-images fix removed the accumulation
  root cause.
- **Tests are deterministic — there are no flakes** (`.claude/rules/rules.md`).
  No wall-clock constant in a test's pass/fail path, ever (not even a generous
  one). Block on the real signal; the suite-level `go test -timeout` is the only
  hang backstop. When a test "flakes," fix its nondeterminism at the root — see
  the Phase-2 determinism track and ledger rows 2364/2365.
- **Never destroy uncommitted work.** Agents are prompted to NEVER run
  `git checkout`/`restore`/`reset`/`stash`/`clean` (one agent clobbered another's
  uncommitted edits this way). Stage progress (`git add`) as checkpoints; the
  index survives a stray working-tree revert. Commit only when the user asks.

## Intent ledger — the adjudication authority

`.ok-planner/design/intent/`: 75 per-concept dossiers distilled from the
project's recoverable design history (session transcripts 2026-06-12..07-13,
ground truth; ok-planner history artifacts 2026-05-04..06-11, lower-trust).
Each dossier has: Net position / Required behaviors (open promises, ~1,184
total) / Intentional absences (~700, never "restore" these) / Corrections &
restorations (drift precedents) / Superseded-historical / Conflicts (now all
RESOLVED with dated user rulings). Every Track 0a ruling records its own Phase 2
proof/build obligation inside its dossier — those obligations travel with the
concept and are not separately listed here.

Adjudicators read a concept's dossier as the intent authority. The dossiers were
written WITHOUT consulting current code or concept docs, so they can be compared
against both without circularity.

## Adjudication model

Every finding is classified by comparing the **intent sources** (dossier, design
corpus, tests — in that precedence) against the **judged subject** (whichever
surface the finding accuses). Code never votes on intent; a dossier entry ruling
"code is right here" is the only way code behavior becomes intent.

| classification | meaning | consequence |
| --- | --- | --- |
| `defect` | intent known; a surface (code/doc/test) fails it | mechanical remediation queue |
| `conforms` | intent known; all surfaces honor it | refute the finding, close the row |
| `design-call` | intent sources disagree, are silent, or are suspect | human discussion queue |

**Provenance gate.** Intent is *known* only when it traces to transcript-tier
evidence or is corroborated across independent artifacts. Artifact-only and
uncorroborated = *claimed* intent, one rung lower.

**Plausibility challenge.** Artifact-only AND design-incoherent → `design-call`
flagged `suspect-canonization`, never treated as known intent.

**Intent-independent defects.** Races, panics, resource leaks, data loss — no
intent makes them correct. Classified `defect` directly, no dossier consultation.
(This is what Track 0b drained for the criticals; lower-severity ones remain in
the main ledger and surface in Phase 3.)

Every ruling records evidence: `classification`, `sources` (e.g.
`dossier=X; doc=Y; code=Z; test=missing`), `intent_provenance`
(`transcript` | `corroborated` | `artifact-only` | `suspect`), `direction`
(`fix-code` | `fix-doc` | `fix-test` | `restore-feature` | `refute` |
`design-call`). Rulings land as JSONL keyed by finding id adjacent to the CSV,
merged into the CSV mechanically — never hand-edited.

## Phases (each phase is the judging surface for the next — do not reorder)

### Phase 1 — design corpus reconciliation (NEXT, ~327 rows)
Categories `design-drift-doc-stale` (204), `design-drift-code-stale` (39),
`index-mismatch` (29), `currency` (23), `adjacency` (17), `design-conformance`
(15), plus any row whose judged subject is a design doc. Adjudicate per concept
cluster against the dossiers; `defect` rows are prose fixes to
`.ok-planner/design/{concepts,stories,decisions,tensions}/` against dossier
citations. Tension files may be resolved/moved ONLY when a dossier ruling
directly answers them (per the 2026-07-14 area--misc ruling), citing the ruling.
Exit gate: plumbline lint + citation resolution green; corpus agrees with the
dossiers everywhere except recorded design-calls.

### Phase 2 — proofs and tests (~91 rows + the feature-loss sweep)
Categories `test-gap` (54), `coverage-gap` (13), `vacuous-test` (12),
`weak-assertion` (12), plus proof-restoration. **Feature-loss sweep is the fabric
of the phase:** every dossier "required behavior" is checked for (a) existence in
code, (b) a guarding test that fails on removal. Misses append `restore-feature`
rows with the dossier citation as spec. Tests asserting drifted behavior are
flipped to intent *now* so they cannot defend drift in Phase 3. Exit gate:
`make test-all` green; every required behavior guarded or queued.

**Test-determinism track** (ledger rows 2364/2365): the systematic sweep of the
`test/scenarios` wall-clock-verdict helpers (`fx.waitForNodeEventKind(...,timeout)`,
`h.WaitForNodeState(...,timeout)`) to deadline-free variants; plus optionally a
lint ban on `time.Sleep`/bare `time.Now()` in `_test.go` and a CI stress gate.
Already fixed at root: the `lib/runtime/hostagent` harness waits, the breakpoint
notify-only polls (`WaitForNodeStateForever`/`waitForHitCountForever`), and
`waitDialable`.

### Phase 3 — code (~490 rows + the restore-feature queue)
`behavior` (265), `structure` (187), `bug` (28), `error-handling` (10), plus the
Phase-2 `restore-feature` queue and the Track-0a rulings' build/retirement
obligations. By now defects surface as red tests against an honest corpus.
Batches are **per concept, not per severity**: one implementer holds the
concept's dossier + adjudicated rows, orders work restore-feature → fix-code,
ships each fix with its guard test and any same-change concept-doc touch-up.
Full verify per rules.md per batch (`make test-all` for core/service changes).

## Fleet mechanics & pacing

- Cluster findings by concept (file path → `@concept:` tags → dossier); an
  adjudicator gets one concept's dossier, its rows, and read access to
  corpus/code/tests. Cluster sizes ~10–25 rows.
- **Batches of ~4 concurrent agents, file-disjoint**, released as predecessors
  finish (the pacing that held all session and avoided session-limit burns).
  Agents leave changes UNSTAGED; the orchestrator verifies and commits.
- Agent prompts MUST forbid destructive git ops and require deterministic tests
  (no wall-clock verdicts).
- After each phase: merge rulings into the CSV, stage, run the gate, report stats.

## Follow-up units (tracked as main-ledger rows, source `track-0b-discovery`)
- **2362** callback/dispatch DRY extraction (structure) — both files now settled.
- **2363** batch-lease-victim clobber residual — needs a proto change
  (`SubScopeDescriptor`/`CommitRequest`) + `runner_subclaim.go`.
- **2364** scenario-suite wall-clock-verdict determinism sweep.
- **2365** `TestExecutor_PKUniqueFails` container-port readiness flake.

## End state
Every main-ledger row closed as `fixed`, `refuted`, or `design-call-resolved`;
corpus, tests, and code agree with the intent ledger; every restored behavior
guard-tested; the dossiers hold the durable rulings. The security ledger is
worked as its own track. This document archives to `.ok-planner/history/` when
the effort completes.
