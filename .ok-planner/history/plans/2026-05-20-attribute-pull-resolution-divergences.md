# Divergences — 2026-05-20-attribute-pull-resolution

Audit of working tree vs. `.ok-planner/plans/2026-05-20-attribute-pull-resolution.md`. Listed in plan-task order. Stylistic / trivial differences omitted.

---

## 1. Task 37 scenario — focused this-frame test replaces the planned two-caller-one-subgraph fixture

**Plan said (Task 37):** Create `test/scenarios/per_run_attributes/subgraph_invocations_test.go` exercising a parent graph with two delegating nodes (`caller-a`, `caller-b`) pointing at a shared subgraph S, then asserting both per-run isolation across invocations AND the carry-rule end-to-end (steps 1–7 listed in the plan).

**What was implemented:** No `subgraph_invocations_test.go` exists. Instead `test/scenarios/per_run_attributes/substitution_test.go` (`TestPerRunAttributes_DownstreamReadsThisFrame`) does a single upstream → single downstream template, fires twice with different stub values across an admin invalidate, and asserts the downstream's `GetLatestByNode` row reflects the second-fire upstream value.

**Inferred reason:** Implementer self-reported scope-down. The substitution-this-frame property is verified more directly by this simpler fixture; the carry-rule + two-caller isolation aspects of the plan's Task 37 are not end-to-end-covered here. (Per-run isolation across two runs of the same node is covered separately by `fanout_leaves_test.go::TestPerRunAttributes_SequentialRunsTwoRows`, see divergence 2.)

---

## 2. Task 36 scenario — sequential reruns of one node replace the planned fan-out leaves test

**Plan said (Task 36):** Test a fan-out parent emitting 3 leaves, each leaf writing a distinct attribute, then assert each leaf's `node_run_id` row contains its own value.

**What was implemented:** `test/scenarios/per_run_attributes/fanout_leaves_test.go::TestPerRunAttributes_SequentialRunsTwoRows` runs a single-node template twice (with admin invalidate between), asserts the two runs produce two distinct `node_run_id` rows with their own data, and that the first row stays readable by run id after the second run.

**Inferred reason:** The most fundamental per-run-keying invariant is "two runs of one node yield two rows," and a sequential-rerun fixture exercises it without the fan-out scheduling machinery. Filename `fanout_leaves_test.go` is now misleading.

---

## 3. Task 38 scenario — fallback-literal test replaces the planned Z-pattern producer-owned-recovery fixture

**Plan said (Task 38):** Build a Z-pattern (`generate-config` ↔ `verify-config` recovery loop with the fallback operator covering first-dispatch absence), then assert across two dispatches.

**What was implemented:** `test/scenarios/per_run_attributes/fallback_test.go::TestPerRunAttributes_FallbackOperator_LiteralFires` is a single-node template whose only `source:` is `{{params.absent | "fallback-fired"}}`; the test asserts the node reaches fresh and the fallback literal lands in `attributes.data`.

**Inferred reason:** The Z-pattern's load-bearing claim — "fallback fires when directive misses" — is exercised by the simpler fixture; the full producer-owned recovery loop wasn't built.

---

## 4. `parseSubstitutionRefsFromAttributes` scans stores selectors and lock names (in addition to attribute schema)

**Plan said:** The function scans attribute-schema `source:` strings only (per spec §4.3 auto-subscribe).

**What was implemented:** `code:graph/node/subscription_edges.go::parseSubstitutionRefsFromAttributes` also iterates `n.Stores[i].Selector` and `n.Locks[i].Name`, calling the same `scanSrc` closure.

**Inferred reason:** Implementer self-reported. Acquisition-time substitution sites (store selectors, lock names) also read `{{nodes.X.attribute.Y}}` directives via `buildLockSpecs` → `BuildAttributeDeps`. If those reads weren't auto-subscribed, the receiver wouldn't have a wait-set row for the referenced upstream and the substitution context (drained-rows only) would miss it. Closing the gap at the auto-subscribe parser is structural rather than a per-site fix.

---

## 5. `SelectCandidates` in both pg and sqlite drivers gained the wait-set NOT-EXISTS gate

**Plan said (Tasks 13/14):** Add `AND w.drained_at IS NULL` to the `NOT EXISTS` subqueries in `ListReadyForDispatch` and `ListPureCascadeReady` only.

**What was implemented:** In addition to those two predicates, both `code:foundation/persistence/postgres/queue.go::SelectCandidates#143-148` and `code:foundation/persistence/sqlite/queue.go::SelectCandidates#126-131` gained a brand-new `NOT EXISTS … w.drained_at IS NULL` subquery (the dispatch row was not previously filtered against the wait-set at all in `SelectCandidates`).

**Inferred reason:** Implementer self-reported. Under mark-don't-delete drain semantics, a stale dispatch row whose wait-set still has undrained gates must not be selected as a dispatch candidate. The plan covered the eligibility-list predicates but missed the queue-candidate-selection predicate; without this gate, the runner would pick up a candidate that's still wait-set-blocked.

---

## 6. `isAttributeSourceDirective` uses `strings.Split` (not `SplitN(body, ".", 3)`)

**Plan said (Task 25.2):**
```go
parts := strings.SplitN(body, ".", 3)
return len(parts) >= 3 && parts[0] == "nodes" && parts[2] == "attribute"
```

**What was implemented:** `code:graph/node/template_validator.go::isAttributeSourceDirective#906-920` uses `parts := strings.Split(body, ".")` and additionally strips an optional fallback `| <literal>` suffix before splitting.

**Inferred reason:** Implementer self-reported. The plan's `SplitN(body, ".", 3)` is a bug — for `nodes.X.attribute.Y` it yields `parts[2] == "attribute.Y"`, so `parts[2] == "attribute"` is false. Bare `strings.Split` makes `parts[2] == "attribute"` work for all field-path forms. The fallback-strip is also needed because `hard_dep: true` is allowed on sources with a fallback (e.g. `{{nodes.X.attribute.Y | "default"}}`), and the gate must look at the directive's source-kind without the literal.

---

## 7. Hard-dep walk extracted into helper `pullHardDepUpstreams`

**Plan said (Task 28.2):** Inline the hard-dep loop directly into `cascadeSubscribersStaleInTx`'s BFS body.

**What was implemented:** A separate helper `code:runtime/runner_terminal.go::pullHardDepUpstreams#544-605` is called from the BFS body. Same semantics; cleaner shape.

**Inferred reason:** Function-size hygiene — the cascade walker is already large and the hard-dep block is logically separable.

---

## 8. Hard-dep cycle detector renamed `detectCycle` → `detectHardDepCycle`

**Plan said (Task 26.1):** Helper named `detectCycle`.

**What was implemented:** `code:graph/node/hard_dep_edges.go::detectHardDepCycle#111` (caller updated to match).

**Inferred reason:** Disambiguating name. The plan name is too generic for a package-level helper.

---

## 9. Fallback chain-rejection error type — `ErrMissingSource` instead of `fmt.Errorf`

**Plan said (Task 29.1):**
```go
return nil, fmt.Errorf("fallback chains not admitted: %q", directive)
```

**What was implemented:** `code:graph/attribute/substitution.go::resolveDirectiveValue` returns `&ErrMissingSource{Directive: directive, Reason: "fallback chains are not admitted"}`.

**Inferred reason:** Implementer chose to channel chain rejection through the same error type as other "directive can't resolve" cases. Subtle behavioral consequence: tests using `IsMissingSource(err)` would treat chain-rejection as a missing source rather than as a fatal grammar error. Tests as written (`TestFallbackOperator_ChainsRejected`) only assert `err != nil`, so this distinction isn't currently load-bearing.

---

## 10. Fallback operator tests moved to a separate file

**Plan said (Task 29.3):** Add the eight `TestFallbackOperator_*` tests to `graph/attribute/substitution_test.go`.

**What was implemented:** A new file `graph/attribute/substitution_fallback_test.go` holds all eight (the planned `TestFallbackOperator_NonMissingErrorIsFatal` was renamed/repurposed as `TestFallbackOperator_MissingDirectiveFallsThroughEvenInvalidShape`, which asserts that `deps.X.Y` falls through to the literal because it returns `ErrMissingSource` — i.e. the "non-missing error is fatal" case isn't directly tested, since with the divergence 9 above, even invalid-shape errors are mapped to `ErrMissingSource`).

**Inferred reason:** Cohesion — the fallback feature is large enough to deserve its own test file. The renamed test reflects the implementation's actual error-classification behavior rather than the plan's stricter assertion.

---

## 11. Wait-set conformance tests folded into existing `testWaitSet` body

**Plan said (Task 12.2):** Add three new top-level conformance test functions — `WaitSetMarkDrainedBySenderRetainsRow`, `WaitSetMarkDrainedBySenderIdempotent`, `WaitSetListDrainedAttributeRowsForReceiver` — and wire them into the dispatch table in `conformance.go`.

**What was implemented:** `foundation/persistence/conformance/wait_set.go::testWaitSet` was extended in place with the three new assertions (retained-after-drain, idempotency by comparing `drained_at` timestamps, list-drained-attribute-rows filter). No new entries in the conformance dispatch table.

**Inferred reason:** Single-table cohesion — keeping the wait-set conformance story in one test function. Side effect: the dispatch table doesn't list these test names separately, so a failure surfaces as `WaitSet` rather than the granular name.

---

## 12. Postgres/SQLite blob-deref logic factored into `scanAttributeRow` helper

**Plan said (Step 6.4):** "factor a private helper `scanAndDeref(row, ctx)` if duplication feels heavy" (optional).

**What was implemented:** Both drivers ship a `scanAttributeRow(ctx, bb, row, op string)` helper that scans + dereferences blobs, called by both `GetByRun` and `GetLatestByNode`.

**Inferred reason:** Took the plan's optional refactor. Helper is per-driver (not in `foundation/persistence/`), preserving driver isolation.

---

## 13. `buildLockSpecs` signature change (`dispatchID, frameID` added)

**Plan said (Task 24.2):** "Replace with a call to `BuildAttributeDeps(ctx, tx, args, acq.DispatchID, acq.FrameID)` — same parameters, same semantics."

**What was implemented:** `code:runtime/runner_locks.go::buildLockSpecs` now takes two extra parameters (`dispatchID, frameID shared.UUID`), threaded through from `tryAcquire`'s `cand.DispatchID, cand.FrameID`.

**Inferred reason:** Necessary mechanical consequence the plan didn't surface — `BuildAttributeDeps` needs both IDs, but the original `buildLockSpecs` only had access to the node row. Caller updated; the only call site (`tryAcquire`) passes them.

---

## 14. CHANGELOG entry merged with the unrelated multi-source-decline entry

**Plan said (Step 40.4):** Append a single bullet under `## Unreleased` describing this spec's changes.

**What was implemented:** Two bullets land under `## Unreleased`: the planned attribute-pull-resolution bullet (with an added extra clause about the wait-set drain change, the auto-subscribe scan extension, and the subgraph carry-rule fix), plus a second bullet covering the prior 2026-05-20 multi-source-substitution decline (which was merged into the same working tree).

**Inferred reason:** Concurrent work — the multi-source-decline change shares the same working tree, so its CHANGELOG entry came along. The added clauses in the primary bullet reflect the actually-landed extras (drain semantics, auto-subscribe scope, subgraph carry).

---

## 15. `apps/` untracked directory in working tree

**Plan said:** No mention of an `apps/` directory.

**What was implemented:** `apps/` shows up as untracked in `git status`.

**Inferred reason:** Out of scope — likely unrelated work or a stray scaffold. Not part of this plan.
