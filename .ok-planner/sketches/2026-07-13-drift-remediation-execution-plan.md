# Drift-Remediation Execution Plan

Adjudicate and remediate the 2,361 findings in `review-findings-2026-07-06.csv` against the design-intent ledger (`.ok-planner/design/intent/`), without collapsing features that drift already weakened and without resurrecting features that were deliberately retired.

## Inputs

- **Findings ledger** — `review-findings-2026-07-06.csv`: 2,361 rows (1,130 CONFIRMED, 1,163 unverified, 66 REFUTED, 114 marked duplicates via `duplicate_of`). REFUTED rows and duplicates are closed/skipped; unverified rows get verified as a side effect of adjudication.
- **Intent ledger** — `.ok-planner/design/intent/`: 75 dossiers distilled from 1,959 entries (transcripts 2026-06-12..07-13 + ok-planner history artifacts 2026-05-04..06-11). 1,184 required behaviors, 700 intentional absences, 42 conflicts needing human ruling.
- **Design corpus** — `.ok-planner/design/{concepts,stories,decisions,tensions}/`: the surface being reconciled in Phase 1; suspect until then.
- **Code and tests** — the subjects being judged; suspect until Phase 3.

## Adjudication model

Every finding is classified by comparing the **intent sources** (dossier, design corpus, tests — in that precedence) against the **judged subject** (whichever surface the finding accuses). Code never votes on intent; a dossier entry ruling "code is right here" is the only way code behavior becomes intent.

Three outcomes:

| classification | meaning | consequence |
| --- | --- | --- |
| `defect` | intent known; a surface (code/doc/test) fails it | mechanical remediation queue, no discussion |
| `conforms` | intent known; all surfaces honor it | refute the finding, close the row |
| `design-call` | intent sources disagree, are silent, or are suspect | human discussion queue |

**Provenance gate.** Intent counts as *known* only when it traces to transcript-tier evidence (user words or user-ratified proposal) or is corroborated across independent artifacts. An artifact-only, uncorroborated claim is *claimed* intent, one rung lower.

**Plausibility challenge.** When the only evidence is artifact-tier AND the claim implies something design-incoherent, the adjudicator must not treat it as known intent — it routes to `design-call` flagged `suspect-canonization`. Type specimen: the issue-1926 story canonizing two independent publishers sharing backing state (ruling pending in a parallel session; fold it into the publisher dossier as precedent when it lands).

**Intent-independent defects.** Races, panics, security holes, resource leaks — no intent can make them correct. Marked `intent-independent`, classified `defect` directly, no dossier consultation needed.

Every ruling records its evidence so it is auditable and re-checkable: for each row, adjudicators write `classification`, `sources` (e.g. `dossier=X; doc=Y; code=Z; test=missing`), `intent_provenance` (`transcript` | `corroborated` | `artifact-only` | `suspect`), and `direction` (`fix-code` | `fix-doc` | `fix-test` | `restore-feature` | `refute` | `design-call`). Rulings land as JSONL keyed by finding id (adjacent to the CSV), merged into the CSV mechanically — never hand-edited into it.

## Ordering

Each phase is the judging surface for the next; running them out of order re-poisons the later phases.

### Track 0a — conflict rulings (user session, front-loaded)
Walk the 42 dossier conflicts with the user; each ruling is written back into its dossier (conflict section → resolved position with date). These gate Phase 1 for their concepts. The 1926 ruling joins here.

### Track 0b — intent-independent defects (parallel, starts immediately)
The confirmed crit/major rows marked `intent-independent` (proxy Register auth, callback channel-close panic, commit idempotency, and kin). Standard fix flow: fix + regression test + full verify per rules.md. No dependency on corpus cleanliness.

**Follow-up units discovered mid-fix (must not be dropped — rules.md "Fix Every Bug You Find"):**
- **Batch-lease-victim clobber (from finding 1761 fix, 2026-07-14).** The filesystem claim-producer fix closed both confirmed single-claim data-loss vectors by matching `findByScope` only to marked batch-pop leases. The residual: when the clobber *victim* is itself a batch lease on a byte-equal folder scope, disambiguation needs a per-lease generation token round-tripped through `proto:claim_producer.proto::SubScopeDescriptor` + `CommitRequest` and a `runner_subclaim.go` change. Deferred only to avoid racing the concurrent acquire-cluster edit of `runner_subclaim.go`; run as a dedicated unit once that file is settled. Cross-module (proto regen + runtime + filesystem store).

### Phase 1 — design corpus reconciliation
Rows in categories `design-drift-doc-stale` (204), `design-drift-code-stale` (39), `index-mismatch` (29), `currency` (23), `adjacency` (17), `design-conformance` (15), plus any other row whose judged subject is a design doc. Adjudicate per concept cluster; `defect` rows are prose fixes against dossier citations. Exit gate: plumbline lint + citation resolution green; corpus agrees with dossiers everywhere except recorded design-calls.

### Phase 2 — proofs and tests
Rows in `test-gap` (54), `coverage-gap` (13), `vacuous-test` (12), `weak-assertion` (12), plus proof-restoration items. **The feature-loss sweep runs here as the fabric of the phase:** every dossier "required behavior" (1,184) is checked for (a) existence in code, (b) a guarding test that fails on removal. Misses append new `restore-feature` rows to the ledger with the dossier citation as spec. Tests asserting drifted behavior are flipped to intent *now* so they cannot defend drift during Phase 3. Exit gate: `make test-all` green; every required behavior either guarded or queued as a known red/restore item.

**Test-determinism track (ruled 2026-07-14, user):** flakes are defects, never tuned away (rule in `.claude/rules/rules.md` "Tests Are Deterministic"). Phase 2 work items: (a) a mechanical lint ban on `time.Sleep` and bare `time.Now()` in `_test.go` files (custom check or golangci forbidigo rule); (b) a CI stress gate that runs the suite under artificial load / `-race -count=N` so timing-fragile tests *surface* rather than hide; (c) a flake-hunt sweep of existing timing-fragile tests. Fixed so far at root: the `lib/runtime/hostagent` harness waits (bare receives), the breakpoint notify-only polls (`WaitForNodeStateForever`/`waitForHitCountForever`), and `test/scenarios/host_agent_harness_test.go::waitDialable` (now blocks until dialable, no deadline). REMAINING (the systematic sweep): the whole `test/scenarios` suite still uses wall-clock-deadline verdict helpers — `fx.waitForNodeEventKind(..., timeout)` and `h.WaitForNodeState(..., timeout)` have dozens of callers across the host-agent and other scenario tests; convert them to deadline-free variants (poll until the condition; suite-level `go test -timeout` the only backstop) as one DRY sweep. Rule: NO finite wall-clock constant in any test's pass/fail path (not even a generous one — see rules.md). Each converted test must pass under the stress gate (artificial load + `-race -count=N`).

### Phase 3 — code
Everything remaining: `behavior` (265), `bug`, `structure`, `error-handling`, and the `restore-feature` queue from Phase 2. By now defects surface as red tests against an honest corpus. Remediation batches are **per concept, not per severity**: one implementer holds the concept's dossier + adjudicated rows, orders work restore-feature → fix-code, ships each fix with its guard test and any same-change concept-doc touch-up. Full verify per rules.md per batch.

## Batching and fleet mechanics

- Findings are clustered by concept (via file path → `@concept:` tags → dossier); an adjudicator gets one concept's dossier, its rows, and read access to corpus/code/tests. Cluster sizes ~10–25 rows.
- Fleet sized to avoid session-limit burns: batches of ~4 concurrent agents, released as predecessors finish (the pattern that recovered the consolidation fleet).
- Adjudication of a concept whose conflicts are un-ruled is deferred, not guessed.
- After each phase: merge rulings into the CSV, stage everything (`git add`), report phase stats before starting the next.

## End state

Every row closed as `fixed`, `refuted`, or `design-call-resolved`; corpus, tests, and code agree with the intent ledger; every restored behavior guard-tested; the dossiers updated with rulings so the ledger remains the durable adjudication record.
