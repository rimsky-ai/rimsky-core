# Divergences — 2026-05-29-console-upstream-auth-audit-and-fixes

Audit of the working tree against what the plan literally said. Scope per the auditor brief: code + the Pass-9 design-doc mutations under `.ok-planner/design/`; other `.ok-planner/` scratch ignored. Build (`go build ./...`) and `go vet` on every touched package pass; no orphaned references to removed symbols remain. This is a record, not a critique — correctness review is separate.

---

## 1. Pass 7 / Task 20 — new tx-aware `ListDeliveredForFrame` method instead of the tx-less `Messages().List(...)`

- **What the plan said:** Task 20 step 1: "fetch the frame's delivered message(s) via the Task 19 lookup: `args.Persist.Messages().List(ctx, persistence.MessageListFilter{FrameID: &frameID}, ...)`." Task 19 added the `FrameID` filter to `List` for exactly this caller.
- **What was implemented:** The `FrameID` filter on `List` was added as specified (Task 19, both drivers, conformance), but it is used only for control-api / observability reads. For the fan-out acquisition path, the implementer added a *new* interface method `ListDeliveredForFrame(ctx, tx, frame)` on `MessagesTable` (`lib/foundation/persistence/messages.go:96`), implemented in both drivers (`postgres/messages.go:108`, `sqlite/messages.go:102`) and the conformance + scenario fakes. The runtime calls it via `triggerMessagePayloadForFrame` (`lib/runtime/runner_acquire_helpers.go:225`).
- **Inferred reason:** Forced choice / correctness. The plan's `List` is tx-less and opens a fresh connection; called from inside the open acquisition transaction it deadlocks the SQLite driver (`MaxOpenConns=1` — the only pooled connection is held by the tx). The tx-aware method reuses the caller's tx. This is the candidate the orchestrator flagged, confirmed exactly. The deadlock reasoning is documented in the interface doc-comment and at the call site.

## 2. Pass 8 / Task 21 — SQLite migration `004` is a documented no-op

- **What the plan said:** Task 21 step 3: "**add** `004-frame-delivery-default.sql` in each driver dir that `ALTER`s `rimsky_instances.frame_delivery_mode`'s default to `'serial_queue'`."
- **What was implemented:** Postgres `004` does the real `ALTER COLUMN ... SET DEFAULT 'serial_queue'`. The SQLite `004` is `SELECT 1;` — an intentional no-op with a long rationale comment: SQLite has no `ALTER COLUMN ... SET DEFAULT`, and the only alternative (table rebuild) is unsafe here because eleven child tables reference `rimsky_instances` with `ON DELETE CASCADE` and the migration runs in a single transaction where `PRAGMA foreign_keys=OFF` is a no-op. The load-bearing default is carried by the INSERT literal (`COALESCE(?, 'serial_queue')`) in both drivers regardless.
- **Inferred reason:** Forced choice (SQLite DDL limitation) + risk avoidance. The column DEFAULT is belt-and-suspenders only; the insert literal is the real default. Confirmed candidate. The SQLite `001` column DEFAULT stays `'coalesce'` and is now dead for the normal create path.

## 3. Pass 4 / Task 14 — `/audit` rejects a lone `?target=` filter with 400 rather than parsing it into the filter

- **What the plan said:** Task 14 step 1 listed `target` (= request_path) among the query params the handler "parses ... into an `EventListFilter`," implying it is an accepted, working filter dimension.
- **What was implemented:** `handleListAudit` (`lib/control/controlapi/audit_read.go:72-82`) rejects any non-empty `?target=` with `400` ("target filtering is not supported; filter by key_id/key_name + action + since"). There is no `request_path` field on `EventListFilter` and no payload index for it (migration `003` indexes only key_id / action / response_status / mode).
- **Inferred reason:** Load-bearing property defended over plan literalness. A `target` filter has no expression index, so honoring it would force an unindexed full payload scan, or silently no-op. The implementer chose explicit rejection over a silent full-scan-with-no-effect — consistent with the spec's overall "reject, don't silently degrade" posture. Confirmed candidate.

## 4. Pass 4 — `/audit` exposes the key-lifecycle event kinds, broader than the plan's example `KindIn`

- **What the plan said:** Task 14 step 1 example: `KindIn: ["auth.access_attempted","auth.access_denied", ...]`.
- **What was implemented:** `auditKinds` (`lib/control/controlapi/audit_read.go:39`) is all five `auth.*` kinds: `access_attempted`, `access_denied`, `key_created`, `key_revoked`, `key_rotated`.
- **Inferred reason:** Spec-intent read. The spec's `event-log` design change frames `audit:read` as a reader of "the `auth.*` rows" generally, and the plan's example ended with "...". Including the key-lifecycle rows rounds out the forensic record. Minor, but the `status`/`mode` payload filters don't apply to those three kinds (their payload lacks those keys), so a status/mode filter narrows them out — benign.

## 5. Pass 4 — only four of the six payload filters are index-backed

- **What the plan said:** Task 13 added six payload filter fields (`KeyID`, `KeyName`, `ActionExact`, `ActionPrefix`, `ResponseStatus`, `Mode`); Task 15 added expression indexes.
- **What was implemented:** Migration `003` (both drivers) indexes `key_id`, `action`, `response_status`, `mode` — but **not** `key_name`. A `key_name` filter therefore runs unindexed. (The plan's Task 15 step 1 example listed exactly these four indexes, so the index set matches the plan's example; the gap is that the filter surface is wider than the index surface.)
- **Inferred reason:** Plan example carried forward verbatim. Not a deviation from the plan's literal index list, but worth recording: the `KeyName` filter the handler accepts is not index-backed, and the `target`/`request_path` dimension is rejected outright (item 3) for the same "no index" reason.

## 6. Pass 5 — shared prune WHERE-clause constant (stronger than the plan asked)

- **What the plan said:** Task 16 step 1-2: add `CountOlderThan` to the interface and "Implement in postgres + sqlite drivers (same WHERE as `DeleteOlderThan`)."
- **What was implemented:** Rather than duplicating the WHERE clause, the implementer extracted it into a single shared constant per driver (`lineagePruneWhereSQL` in `postgres/lineage.go:147`, `sqliteLineagePruneWhereSQL` in `sqlite/lineage.go:143`) and concatenated it into both the `DELETE` and the `SELECT count(*)` statements, so the two statements physically cannot drift.
- **Inferred reason:** Cleaner shape that *strengthens* the load-bearing "true preview, not an approximation" property — the plan only said "same WHERE," the implementer made divergence structurally impossible. Recorded as a positive divergence.

## 7. Pass 5 — prune dry-run uses an explicit mode check + `WriteDryRunResponseForced`, not `WriteDryRunResponse`

- **What the plan said:** Task 17 step 1: dry-run branch calls `CountOlderThan` and returns `WriteDryRunResponse(w, req, "would_have_pruned", {"before", "count": n})`.
- **What was implemented:** `handleLineagePrune` (`lib/control/controlapi/lineage.go:107-118`) checks `ModeFromContext(...) == authModeDryRun` explicitly, then writes via `WriteDryRunResponseForced`. The stated reason: so the `CountOlderThan` query only runs in dry-run mode (rather than running it unconditionally and passing the result into the combined helper).
- **Inferred reason:** Cleaner shape — avoids running the count query on the live path. Behaviorally equivalent envelope; just a different control-flow shape than the plan's one-liner.

## 8. Pass 2/5/6 — dry-run branches reuse the pre-existing `errDryRunOK` tx-rollback pattern where the mutation sits inside a transaction

- **What the plan said:** The plan's snippets for breakpoint:create (Task 6) and the auth/instance writes showed the simple `if WriteDryRunResponse(w, req, ...) { return }` pre-mutation gate.
- **What was implemented:** For handlers whose validation is coupled to an open transaction (breakpoint:create with its `FOR UPDATE` template lock — `breakpoints.go:213,252`; breakpoint:delete — `:394,398`; backfill:create — `backfills.go:172,187`), the implementer returns the pre-existing `errDryRunOK` sentinel from inside the tx (rolling back the lock) and writes the envelope via `WriteDryRunResponseForced` outside. The simple gate is used only where no enclosing tx is needed (instance:pause/resume, breakpoint:resume, auth:revoke/rotate). `auth:create` uses `WriteDryRunResponseForced` directly per the plan's own snippet.
- **Inferred reason:** Cleaner/more-correct shape, reusing infrastructure (`errDryRunOK`, `authModeDryRun`, `WriteDryRunResponseForced`) that already existed for `instance:create` / `templates` / `messages` / `assets`. The plan under-specified the tx interaction; the implementer kept the family consistent. Net effect — a previewed breakpoint:create rolls back the `FOR UPDATE` lock — is stronger than a naive pre-tx gate.

## 9. Pass 3 — synchronous audit insert detaches from request-context cancellation

- **What the plan said:** Task 10 step 2: change `emitAttempted`/`emitDenied` to call `insertEvent` directly (synchronously).
- **What was implemented:** `insertEvent` (`lib/control/controlapi/audit.go`) ignores its passed `ctx` (param renamed `_`) and runs the transaction under `context.WithTimeout(context.Background(), auditWriteTimeout)`. So a client disconnect / request-context cancellation does **not** abort the audit write.
- **Inferred reason:** Durability over latency — deliberate and consistent with the pass's load-bearing property ("never silently dropped"). The plan did not specify the cancellation behavior; the implementer resolved the unspecified call by spending a bounded slice of request-goroutine time (`auditWriteTimeout`) to guarantee the forensic row lands even if the caller has gone away. The tradeoff is documented in the `insertEvent` doc-comment and `@blessed-invariant`.

## 10. Pass 7 — the "override changes partitions processed" regression test landed as a `lib/runtime` unit test, not a full-stack scenario test

- **What the plan said:** Task 20 verification: `go test ./lib/runtime/... ./test/scenarios/... -run 'Backfill|FanOut'` — "a backfill on a `serial_queue` instance processes the **override's** partitions, not the template default (the regression test for the silent bug)," naming `test/scenarios/...` as a target.
- **What was implemented:** The regression pin is `TestSubstituteFanOutPartitionRequest_OverrideBindsFromTriggerMessage` (`lib/runtime/runner_acquire_helpers_test.go:94`), a unit test that drives `substituteFanOutPartitionRequest` directly and asserts the substituted bytes carry the override's partition keys (not the template default), plus a strict-directive-refuses-without-trigger case. The pre-existing `test/scenarios/backfill/partition_selector_override_test.go` was **not** extended — it still only pins the enqueue side (override rides into the message payload verbatim), not that the override reaches `SplitScope` at acquisition.
- **Inferred reason:** Pragmatic placement. The unit test exercises the exact load-bearing assertion the spec demanded (override binds and changes the bytes handed to the split) without standing up a full testcontainers fan-out acquisition. The guard exists and is meaningful; it just lives a layer below where the plan's verification line pointed, and no new full-stack scenario test was added for the override path.

## 11. Pass 8 — `coalesce` conflict detection has a nil-resolver "deliver everything" escape hatch

- **What the plan said:** Task 22 made coalesce conflict-aware; the spec error-handling table treats coalesce-with-multiple-conflicting as the case that must split into frames.
- **What was implemented:** `coalesceDeliverSet` (`lib/runtime/message_delivery.go`) takes a `coalesceConflictResolver`; when the resolver is `nil` it delivers *all* pending messages (legacy "coalesce delivers everything"). The resolver is `nil` only when there is no template to load (the pure-unit fakes) — production always builds one via `buildCoalesceConflictResolver`. The two pure-unit message tests (`TestFrameDeliveryMode_CoalesceDeliversAll` and the dead-letter/multi-receiver/operator/sensor unit tests) pass `nil` and thus do not exercise conflict-awareness.
- **Inferred reason:** Forced accommodation of the unit-test fakes (no template available). The plan/spec did not mention a nil-resolver path; the implementer added it so the pure fakes keep compiling/passing under the new signature. Production behavior is unaffected (resolver always present there). Recorded because the unit-level coalesce tests now assert only the legacy path.

## 12. Pass 8 — exported `DeliverPendingMessages` signature changed (new trailing `resolve` parameter)

- **What the plan said:** Task 22 described restructuring `DeliverPendingMessages` / `deliverForRunningFrame` to make match info available when choosing the deliver-set; it did not specify the function signature.
- **What was implemented:** `DeliverPendingMessages` gained a trailing `resolve coalesceConflictResolver` parameter. Every caller (production `deliverForRunningFrame`, plus six message scenario/unit tests) was updated; tests pass `nil`.
- **Inferred reason:** Cleaner shape — passing the resolver in keeps the conflict decision in the delivery function rather than pre-filtering the message list. Records the cross-package signature ripple (the fix-forward of the six test call sites is mechanical).

## 13. Out-of-scope bug fixes folded in (per "Fix Every Bug You Find")

The plan did not call for these; the implementer fixed pre-existing bugs found while working:

- **`mcp_resources_test.go` parallel-flake** — ten `TestResources_*` tests dropped `t.Parallel()` because `withIdentity` installs a *process-global* MCP identity hook (`mcp.SetIdentityHook`); parallel siblings raced each other's cleanup-restore, intermittently surfacing as "permission denied." The orchestrator flagged this; confirmed. Fixed by serializing every caller, with a docstring explaining why.
- **`auth_common.go::applyGrantPatches` / `auth_list.go::grantsEqual`** — dropping `GrantEntry.Mode` forced edits beyond the files the plan named: `auth_list.go:111`'s `grantsEqual` compared `.Mode`; it now compares `Action` only. Mechanical compile-coupling, not named in the plan's Task 3 file list.
- **`fan-out.md` / `frame.md` doc-grammar bug** — the `| default: <x>` directive grammar (no such keyword in the engine) was corrected to `<directive> | <literal>` per the plan's own Task 23 step 6, but the same correction surfaced consistency edits in adjacent prose.

## 14. Pass 9 — `node-subscription.md` 2026-05-20 Notes entry corrected *in place* in addition to the appended entry

- **What the plan said:** Task 24 step 2: "Correct the 2026-05-20 '... push ...' line ... Notes entry 2026-05-29."
- **What was implemented:** Both — the implementer rewrote the body of the existing dated 2026-05-20 Notes entry (adding a parenthetical "Correction 2026-05-29: ...") **and** appended a fresh 2026-05-29 Notes entry. The Notes section is meant to be append-only per `.ok-planner/CLAUDE.md`.
- **Inferred reason:** Ambiguous instruction resolved toward maximum clarity. "Correct the ... line" reads as editing the body text (which happens to live in a prior Notes entry); the implementer satisfied that literally while also preserving the append-only audit trail with a new entry. Minor process deviation, recorded for the design-doc compliance reviewer.

## 15. Pass 9 — `dry-run.md` Boundaries no longer enumerates the per-handler dry-run branches

- **What the plan said:** Task 23 step 1: rewrite "Boundaries" so dry-run "covers all writes; no auth carve-out; no per-grant mode vocabulary."
- **What was implemented:** The old Boundaries text contained a full enumeration of handler branches (instance:create, template:*, tag:*, ...); the rewrite replaced the enumeration with "Dry-run covers **all** write actions uniformly — there is no auth carve-out." The concrete list was dropped rather than updated.
- **Inferred reason:** Spec-intent read + self-containment. The "structural guarantee / no carve-outs" framing makes the enumeration redundant (and the coverage conformance test is now the authority on which actions are covered). Consistent with the plan's "covers all writes" direction; just records that the enumerated list was removed, not maintained.

## 16. Pass 9 — tension `category` set to `unspecified`

- **What the plan said:** Task 24 step 3 specified `status: open` and `affects: [named-event, node-subscription]`; it did not name a `category`.
- **What was implemented:** `event-vocabulary-implies-delivery.md` frontmatter carries `category: unspecified`.
- **Inferred reason:** Plan was silent on the field; the implementer supplied a placeholder rather than omitting it. The `## Resolution candidates` section is path-free as required (no sketch/spec/file references), and the deferred-rename sketch is deliberately *not* cited in the tension body per the plan's explicit instruction.

---

## Confirmed faithful (no divergence)

- Pass 1 — `GrantEntry.Mode` dropped, `CheckResult.Mode` dropped, set-membership evaluator, `Mode`/`ModeExecute`/`ModeDryRun` request vocabulary retained, JSON `mode` key tolerated via `Extras`. CLI `--dry-run` flag removed. (`CheckResult.MatchedIdx` retained — plan only said drop `.Mode`.)
- Pass 2 — `?dry_run=true` → `ModeDryRun`; `isWrite` via `Registry.Entry(action)`; `executed = status < 400 && (!isWrite || mode == execute)`. Auth-mutation carve-out removed; `auth:create` dry-run mints no plaintext + anonymous-mode note. Coverage conformance test added.
- Pass 3 — `auditDispatcher` + queue/workers + `EnsureAuditDispatcher`/`StopAuditDispatcher`/`dispatcher()` + `auditDisp` field + config call sites all deleted; `insertEvent` synchronous; `@blessed-invariant` added. No orphaned references remain.
- Pass 4 — `audit:read` action + `GET /audit` + role-template coverage (operator.json) + `registerAuditRoutes` registered in `app.go` and the synthetic router in `TestRegistryCoversRouter`. Both-driver payload filters + conformance test wired.
- Pass 6 — backfill target validation rejects non-existent / non-fan-out / non-trigger-wired targets at `400` on both live and dry-run paths, via the new `attributes.ReferencesTriggerMessage` detector (matches on `TemplateNodeDef.Type`, consistent with `runtime/backfill.go`).
- Pass 8 / Task 22 — conflict-aware coalesce reuses `ExtractSubstitutionRefsFromTemplate` / `BuildSubscriptionEdges`; conservative comparison (any payload difference among messages matching a common receiver type = conflict) as the spec explicitly authorized; received-order preserved; serial_queue unchanged. Default flipped via both insert literals + code fallback.
- Pass 9 — all eight concept edits + the two accuracy fixes + the tension + the regenerated TOC match the spec's `## Design changes` bullets. Notes entries dated 2026-05-29 present in every edited concept. TOC one-line defs for `dry-run` and `permission` updated; list re-alphabetized (incidental `sdk`/`schedule` reorder).
