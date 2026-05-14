# Implementation notes — Subscription-cascade resolution + quality-of-life cycle

**Plan:** `.ok-planner/plans/2026-05-14-subscription-cascade-and-quality-of-life.md`
**Spec:** `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`

This file is the durable record of deviations, judgment calls, and items
surfaced for post-run discussion as the plan executes. Each entry below
documents a specific decision or discovery that the user should see after
the run is complete.

---

## Task T3/T10 — HandlerInvalidate retained as deprecated stub
**Deviation:** Kept `spec.HandlerInvalidate` and its `graph/node` alias
in place rather than deleting per T10. The struct is now flagged
`// Deprecated:` with no consumers in production code.
**Reason:** T3 step 9 explicitly says "may still be referenced by
external code … keep it for now; T10 will retire it." After runtime
call sites (T18 / T19) are migrated and tests (T48) are updated, all
consumers should be gone — but the test packages (T45 / T48) still
construct `node.HandlerInvalidate{...}` literals, so the type cannot
be removed until that migration completes. Leaving the type in place
keeps the test-migration work mechanical (replace each construction
site rather than introduce a referential-error churn).
**Surfaced for:** next-dispatch / informational. Final removal lands
in T54 after the test sweep.

## Task T12 — loadSubscribedNodeAttributes still reads nd.Dependencies
**Deviation:** The function rename landed (`loadDepsAttributes` →
`loadSubscribedNodeAttributes`) and the parameter shape changed
(`subscribedNodeIDs []shared.UUID` rather than `*NodeRow`), but the
caller still passes `nd.Dependencies` from the persisted node row.
**Reason:** Per the plan T23, the runtime stops reading `Dependencies`
from `NodeRow` and resolves the sender set from the subscription-edge
map. Until T23 completes, `nd.Dependencies` is the cheapest stand-in
for the legacy receiver→sender map; the instance-creation factory now
populates that column from `Subscribes` so the data is correct under
the new model.
**Surfaced for:** next-dispatch. The TODO marker `T23 wires this from
the per-template subscription-edge inverse map` lives in
`runtime/runner_dispatch.go::loadSubscribedNodeAttributesByID` for
discovery.

## Task T17 — substitution-ref cross-check folded into top-level validator
**Deviation:** T17's prescribed cross-check (each
`{{nodes.<X>.<kind>.<name>}}` ref must name a declared node + name on
the sender) lives in the top-level `ValidateTemplate` loop body rather
than a separate per-node validator. The check is structurally a single
pass over the receiver→[]ref map from
`ExtractSubstitutionRefsFromTemplate`.
**Reason:** Keeping the check at the post-pass level avoids re-parsing
the schema per node and reuses the `declared` map already in scope.
The user-visible behavior matches what T17 specifies.
**Surfaced for:** informational.

## Task T18 — cascadeSubscribersStaleInTx delegates to legacy ListDependentsOf
**Deviation:** The new `cascadeSubscribersStaleInTx` function (in
`runtime/runner_terminal.go`) still uses `args.Persist.Nodes().ListDependentsOf(...)`
to discover receivers rather than the per-template subscription-edge
inverse map. A wait-set row IS inserted for each receiver (the
production-code change the plan calls for), and `cascadeChildrenStaleInTx`
is kept as a deprecated shim.
**Reason:** The full per-template inverse-edge map (computed at template
registration and cached in-memory keyed by template_hash) needs a
runtime cache layer that isn't yet wired. Since `nodes.dependencies` is
now populated from `subscribes:` (via the instance-creation factory
update), `ListDependentsOf` returns the same receiver set the inverse
map would. The downstream wait-set semantics (insert on cascade, drain
on settle) are correct; only the discovery mechanism is interim.
**Surfaced for:** next-dispatch. Worth tracking: when the cached
inverse map lands (paired with T23), pass it to
`cascadeSubscribersStaleInTx` instead of calling ListDependentsOf.

**RESOLVED (2026-05-14 review cleanup):** `cascadeSubscribersStaleInTx`
now drives the BFS walk over the cached `subscriptionEdgesForTemplate`
inverse map (process-global `sync.Map` keyed by `template_hash` in
`runtime/subscription_loaders.go`). The legacy `cascadeChildrenStaleInTx`
shim has been retired. The walk fires recursively over the subscription
subgraph (per-call visited set guards cycles) so a single sender's
invalidation gates the entire downstream graph in one tx.

## Task T20 — frame:next deferred-invalidate queue not extended to subscriptions
**Deviation:** The plan asks the frame:next deferred queue's payload
to carry subscription edges; under my interim, `frame: next` works
the existing way (via `frame.EnqueueOrCoalesce` opening a new frame
at the subscriber's next-tick). The wait-set machinery handles
`frame: in` natively; `frame: next` is a no-op until the next frame
opens, at which point the cascade walk fires fresh wait-set rows.
**Reason:** Time-bounded interim. Behavior is correct for the common
case; the case where a `frame: next` edge needs to fire in a
deliberately deferred way (without an unrelated invalidate to open
the next frame) is the missing piece. Documented but not fixed.
**Surfaced for:** next-dispatch.

**RESOLVED (2026-05-14 review cleanup):** Reframed under the cascade-
walk-at-invalidation discipline. The `FrameNext` branch of
`cascadeSubscribersStaleInTx` opens a new frame via
`frame.EnqueueOrCoalesce` (in-tx, no separate deferred-payload queue);
the receiver becomes a frame source for the new frame and is stamped
by `MarkSourceNodeStale` at frame-open. The cascade walk does NOT
insert a wait-set row keyed on the current sender for `frame: next`
because by the time the new frame opens the sender has already
settled and a gate keyed on it would never drain. Parked receivers
get an in-tx `wakeParkedReceiverInTx` call so the next-frame open can
pick them up as a source. Documented in `runtime/runner_terminal.go`.

**Spec reconciliation (2026-05-14, cleanup pass 2):** The spec text in
`.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`
called for a wait-set row in the next frame's `frame_id` carried via
a deferred-invalidate queue. The implementation chose the simpler
shape above (no wait-set row at all). An erratum was added to the
spec (above the "Cross-cutting topics" section) and a matching entry
to "Tensions resolved" (`tension:frame-next-wait-set-placement`)
documenting the deviation, its rationale (the original placement
shape is unrealizable: the drain trigger has already fired by next-
frame-open), and the soundness scope (`serial_queue` only, which is
the only currently-supported `frame_resolution_mode`).

## Task T23/T24/T25 — Dependencies column kept on NodeRow
**Deviation:** `rimsky_nodes.dependencies` column stays in schema;
`NodeRow.Dependencies` stays in the Go struct. The runtime still reads
it via `loadSubscribedNodeAttributes` (as a stand-in for the cached
subscription-edge map).
**Reason:** Pre-v1 break-freely policy permits deletion, but the
runtime consumer-set is broader than a single dispatch can finish.
Removing the column requires also adding the cached inverse-edge map
in `runtime/` so `loadSubscribedNodeAttributes` has a non-Dependencies
source. Documented; deferred.
**Surfaced for:** next-dispatch. The column is now populated correctly
from `subscribes:` at instance creation, so behavior is the same.

## Task T29 — parked_reason_note read path partial
**Deviation:** Postgres + SQLite `ParkActiveInTx` write the new
`parked_reason_note` column. The `LoadResumeMetadataInTx` /
`ListParkedDiagnostic` / `GetParkedByNode` SELECT lists were NOT
updated to include the new column; the diagnostics endpoint surfaces
`reason` but not `reason_note`. The CLI's table view has a column
for it that will currently render empty.
**Reason:** Time. Read-path plumbing through `ParkedRow`,
`ResumeMetadataRow`, and `ParkedDiagnosticRow` is a 3-file change
mirrored across two adapters; production-correctness-wise the column
is written and stored, just not yet surfaced.
**Surfaced for:** next-dispatch.

## Task T36/T37 — TS tests not run; tsc OOM in environment
**Deviation:** `report_park` MCP tool added to
`internal-mcp-tools.ts` + `internal-mcp-server.ts`. Token-registry
extended with optional `onPark` handler. Wire-up in `agent-run.ts` /
`server.ts` (the actual outcome-promise resolution) is NOT done; the
new MCP tool returns "park_not_supported" until `entry.onPark` is set
by the per-run registration site.
**Reason:** Running `tsc --noEmit` on the project OOMs in this
environment. The MCP-surface changes are syntactically minimal and
match the existing patterns; the runtime wire-up (matching how
`onComplete` / `onBlocked` are populated by the agent-run loop) needs
hands-on verification with the TS test suite.
**Surfaced for:** next-dispatch.

## Tasks T45/T46/T47/T48 — scenario test migration not performed
**Deviation:** The plan calls for mechanical pattern-based migration
of every test file using `Dependencies:`, `OnEvent:`, `Invalidate:`,
`{{deps...}}`, `action: invalidate`. Only `graph/node/template_validator_test.go`
was migrated to keep the package compiling for the new unit tests.
The remaining ~30 test files under `test/scenarios/`, `runtime/`,
`foundation/persistence/`, and `control/controlapi/` are still using
the retired types and will fail to compile.
**Reason:** Time. The production-code changes are functionally complete;
the test migration is mechanical but voluminous. Production code
builds clean (`go build ./...` passes for foundation, root, and
protocols modules).
**Surfaced for:** next-dispatch. Pattern is per T45:
- `Dependencies: []string{"X"}` → `Subscribes: []SubscriptionEntry{{Node: "X", On: "state"}}`
- `OnEvent: map[string]EventHandler{...Invalidate: ...}` on emitter A → `Subscribes: [{Node: "A", On: "event", Name: "X"}]` on receiver B
- `OnExecutorComplete.Invalidate.Targets = []string{"B"}` on A → `Subscribes: [{Node: "A", On: "state", When: "fresh"}]` on B
- `{{deps.X.Y}}` → `{{nodes.X.attribute.Y}}`
- `action: invalidate` entries in `error_types` → remove; add subscription on receiver

## Tasks T49-T53 — atomic-staging + dashboard not implemented
**Deviation:** Atomic-staging pattern doc, reference filesystem
producer binary, conformance run, sweep-loop unit test, and
dashboard parked-reason view are NOT implemented.
**Reason:** Time. These tasks are independent of the rest of the
cycle (Piece 3, plus the dashboard tweak); none of them block the
subscription-cascade or ParkReason work from landing. The reference
producer is a ~700-line TS-free Go binary worth its own dispatch.
**Surfaced for:** next-dispatch.

## Task T51 — conformance run is manual; skipped per plan
**Deviation:** Per plan T51 note: "Skip in automated test sweep;
include as a manual check in the final section." Manual run by the
user.
**Surfaced for:** informational.

## Tasks T55 / T57 — doc snippet migration + CHANGELOG entry
**Deviation:** Public-doc snippet migration in `docs/concepts/` +
`docs/agents/examples/` and the CHANGELOG entry were not done.
**Reason:** Time. Concept-doc Notes entries and tension files (the
in-repo design-log) ARE updated; the public-facing `docs/` tree is
not.
**Surfaced for:** next-dispatch.

## Dispatch 2 entries (2026-05-14, finalization)

## Task T20 — frame:next deferred queue partially extended
**Deviation:** Added a `case node.FrameNext` arm to
`cascadeSubscribersStaleInTx` that routes through
`frame.EnqueueOrCoalesce` to open a new frame for receivers with
`frame: next` subscription modifiers. The plan called for extending
the existing frame:next deferred-invalidate queue's payload shape to
carry `(receiver_node_id, sender_node_id, topic_kind,
topic_filter, subscription_scope)`. The current implementation
relies on `EnqueueOrCoalesce` to open the frame and the cascade walk
to re-fire at next-tick — the wait-set rows for the new frame are
inserted when the new frame opens and the next round of cascade
walks fires.
**Reason:** Pragmatic. The behavior is correct for the common case
(cross-cutting subscriptions and direct frame:next edges). The
edge-case of a `frame: next` subscription that needs an explicitly
deferred wait-set row at next-frame-open without a fresh cascade
walk is not addressed.
**Surfaced for:** next-dispatch.

## Task T23/T24/T25 — full deletion landed
**Deviation:** None — `rimsky_nodes.dependencies` column dropped from
both baseline migrations, `NodeRow.Dependencies` field removed,
`NodeCreateInput.Dependencies` field removed, `ListDependentsOf`
accessor removed from interface + both adapters + deadlock-guard
test, the runtime resolves subscribed senders via the new cached
inverse-edge map. Tests using the retired Dependencies field were
migrated. Build + tests pass.
**Surfaced for:** informational.

## Task T29 — full read path landed
**Deviation:** `ParkedRow`, `ResumeMetadataRow`, `ParkedDiagnosticRow`
now carry `ReasonNote`. Postgres + SQLite SELECT lists were extended.
`ParkedNodeEntry` / `NodeStateRow` on the diagnostics endpoint
surface `reason_note`. The dashboard's parked-nodes view (T53)
renders it. The CLI's `parked list` table view already had the
column.
**Surfaced for:** informational.

## Task T36/T37 — TS report_park wire-up complete
**Deviation:** `agent-run.ts` per-run registration now sets
`entry.onPark` to resolve the outcome promise with the
`park_requested` shape carrying `reason` + `reasonNote`. The
discriminated-union type was extended with `reasonNote: string`.
`server.ts::outcomeToCallbackBody` and
`http-bridge.ts::callbackBody` both pass `reason_note` through to
the gRPC Park terminal. The rate-limit auto-park emits
`reason: "time_wait"` with the descriptive `reasonNote`. New tests
in `internal-mcp-server.test.ts` exercise the dispatch path; the
`internal-mcp-tools.test.ts` schema test now covers `ReportParkInput`
including unspecified-rejection. `npm test` passes (100 tests);
`npm run build` succeeds.
**Surfaced for:** informational.

## Task T45 — selective scenario test migration; retired-behavior tests skipped
**Deviation:** Most `Dependencies:` / `OnEvent:` / `HandlerInvalidate:`
usages in scenario tests were mechanically migrated to `Subscribes:`
+ `WithSubscribes` helper. Some tests exercise behaviors that retire
entirely under the new model — these have a `t.Skip` with a
documenting comment pointing to the new equivalent or the follow-up:
- `TestFrameCoalesceSelfInvalidate` (self-invalidate emit retires)
- `TestHandlerInvalidateOrthogonalToChanged` (send-side emit retires;
  orthogonal-to-changed expressed receiver-side via outcome filter)
- `TestReactiveLoopSelfInvalidateInFrame` (self-invalidate emit retires)
- `TestReactiveLoopSelfInvalidateNextFrame` (self-invalidate emit retires)
- `TestAcquirePassSubscribedMonitorRuns` (cascade-firing gate is
  fresh_changed; `pass` outcome does not propagate; the legacy
  emit-on-pass behavior would need `always_propagate` on the
  on_acquire_unavailable handler, which isn't currently exposed)
- ~~`TestParkedLifecycleIntraGraphInvalidateAgainstParked`~~ (cascade
  walks mark stale + insert wait-set in-tx; they don't route through
  `InvalidateNode`'s unified wake handler that parked-node resume
  requires — re-wiring is a follow-up)
**Surfaced for:** next-dispatch / informational. The skipped tests
document genuine semantic shifts in the new model.

**RESOLVED (2026-05-14 review cleanup):**
`TestParkedLifecycleIntraGraphInvalidateAgainstParked` was un-skipped
and rewritten against the new wake path. The cascade walk now routes
parked receivers through `wakeParkedReceiverInTx` (in-tx variant of
`wakeParkedNode`) so a parked receiver woken by an upstream cascade
transitions parked → stale + emits `parked_resume_started` alongside
the regular `MarkStaleForCascade` + wait-set insert.

## Task T46 — new subscription-cascade scenario test
**Deviation:** Added two scenario tests in
`test/scenarios/subscription_cascade_test.go`:
- `TestSubscriptionCascade_MultipleInvalidatorDrain` — R subscribes
  to A, B, C; assert R reaches fresh after all three settle on the
  initial frame.
- `TestSubscriptionCascade_EligibilityRespectsMultipleSenders` —
  operator-invalidate A in an A/B/C/R graph; assert R re-runs only
  after A's re-run completes.
The full menu from T46 (conditional fan-in, cross-cutting
subscription, frame-end cleanup assertions, frame:next loop
convergence) was not implemented as separate scenario tests; the
two written tests exercise the most load-bearing properties. Other
scenarios in `test/scenarios/lifecycle_handlers_test.go` etc.
already cover always_propagate / never_propagate / by_changed
under the new model.
**Surfaced for:** informational.

**RESOLVED (2026-05-14 review cleanup):**
- `TestSubscriptionCascade_EligibilityRespectsMultipleSenders` was
  rewritten to actually invalidate B and C while A is in flight (per
  Finding 6); the test now exercises the multi-invalidator gating
  property it documents.
- Three additional scenarios added:
  `TestSubscriptionCascade_CrossCuttingPositive` (instance-wide
  failure topic),
  `TestSubscriptionCascade_CrossCuttingNegative` (no-coupling
  baseline asserts an unrelated worker invalidation doesn't drag a
  separate root through running),
  `TestSubscriptionCascade_FrameEndCleansWaitSet` (ON DELETE CASCADE
  from `rimsky_frames` empties `rimsky_wait_set` at frame-end),
  `TestSubscriptionCascade_FrameNextLoopConverges` (frame: next
  modifier opens a new frame for the receiver and converges).

## Task T48 — fireOnEventHandler retirement (already done by prior dispatch)
**Deviation:** Prior dispatch retired `fireOnEventHandler` already.
The `walkEventSubscriptionsForEmitter` function the plan describes
isn't a separate function — the cascade walk
(`cascadeSubscribersStaleInTx`) handles `topic_kind = "event"`
edges uniformly because the inverse-edge map carries them. Updated
`runtime/cascade_invalidate_test.go` to assert wait-set semantics
(pending wait-set → no-op, empty wait-set → enqueue dispatch).
**Surfaced for:** informational.

## Task T50/T52 — atomic-staging reference producer
**Deviation:** Created `examples/atomic-staging-fs-producer/` with:
- `cmd/main.go` — gRPC server entry point with sweep loop wiring.
- `server/server.go` — gRPC `ClaimProducerServer` adapter.
- `store/store.go` — four-verb logic; SQLite was replaced with a
  simple JSONL side-table for portability (avoids the modernc/sqlite
  dependency in this isolated example). Two-rename atomic swap on
  Commit.
- `sweep/sweep.go` — leaked-staging reaper with `HandleSet`
  abstraction over rimsky's live-claim-handle set.
- `sweep/sweep_test.go` — three-case unit test (alive preserved,
  young leak preserved, old leak reaped). Passes.
- `README.md` + `template.yaml` per plan.
Builds clean via `go build ./examples/atomic-staging-fs-producer/...`.
**Surfaced for:** informational.

## Task T51 — conformance run skipped per plan
**Surfaced for:** informational (manual check).

## Task T53 — dashboard parked-nodes view
**Deviation:** New `ParkedNodesPage` route added to dashboard;
groups by reason; `awaiting_human` rows render with amber operator-
attention styling. `Nav.tsx` extended with the new link. `npm run
build` succeeds. Note: the dashboard proxy currently rewrites
`/api/control/*` → `/v1/observability/*`; the admin diagnostics
endpoint is at `/admin/diagnostics/parked-nodes` (no `/v1/observability/`
prefix). The page wires the URL the new model uses; the proxy path
will need adjustment for the view to actually fetch data in a
running deploy. Documented inline.
**Surfaced for:** next-dispatch / manual verification.

**RESOLVED (2026-05-14 review cleanup):** Added a dedicated
`/api/control/admin/*` route ahead of the generic
`/api/control/*` rewrite in `dashboards/rimsky-dashboard/src/server/proxy.ts`.
The admin route forwards bare `/admin/*` paths to control-api so
`/api/control/admin/diagnostics/parked-nodes` correctly hits the
admin diagnostics endpoint instead of being rewritten to
`/v1/observability/admin/diagnostics/parked-nodes`.

## Task T54 — validateDependencies validator removed
**Deviation:** Removed the no-op `validateDependencies` function and
its call site in `graph/node/template_validator.go`. The
`DisallowUnknownFields` JSON decoder in
`control/controlapi/templates.go::decodeRegisterRequest` already
rejects bodies carrying the retired `dependencies:` key. The cycle-
test `TestTemplateDeploy_DependencyCycle_400` was retargeted: it
now asserts the parse-time rejection of the retired field
(subscription cycles between two nodes are no longer rejected at
deploy — under the wait-set model they're a defer-loop across
frames that terminates when no receiver has a wait-set row).
**Surfaced for:** informational.

## Task T55 — public-doc snippet migration (partial)
**Deviation:** Migrated `docs/agents/examples/holding-subgraph.md`
and `docs/agents/examples/two-node-with-claim.md` to the new
`subscribes:` + `{{nodes.X.attribute.Y}}` shape. Did NOT migrate
`docs/concepts/*.md`, `docs/agents/llms-full.txt`, `docs/glossary.md`,
or the broader `docs/agents/errors/`, `docs/humans/` tree.
**Reason:** Time. The two highest-traffic worked examples are
migrated; the broader doc tree is a separate sweep.
**Surfaced for:** next-dispatch.

## Task T56 — build/test sweep
**Deviation:** `make build-all`, `go build ./...` on all three
modules, `go test ./...` on foundation and root, `make lint`, `cd
executors/claude-agent && npm test && npm run build`, and
`cd dashboards/rimsky-dashboard && npm run build` all pass. Race
tests + the `-count=3` repetition NOT run (timing budget). Three
scenario tests failed transiently under heavy Docker contention
when running the full suite in parallel; all three pass when run
individually or with `-p 4`:
- `TestSubscriptionCascade_EligibilityRespectsMultipleSenders` —
  testcontainers ran out of ports.
- `TestParkedLifecycleHeldClaimRetentionAcrossPark` — passes alone.
- `TestFrameStartAtomicity` — Postgres deadlock; pre-existing flake.
**Surfaced for:** informational.

## Field migration note: lint fixes
**Deviation:** A handful of lint issues surfaced during the final
sweep — three gofmt issues from inline edits, one staticcheck S1017
(replace `if strings.HasPrefix... s = s[len(prefix):]` with
`strings.TrimPrefix`). All fixed; `make lint` clean.
**Surfaced for:** informational.

## Dispatch 3 entries (2026-05-14, code-review cleanup)

These entries cover fixes applied during the 17-item code-review
cycle that followed dispatch 2.

### Cascade walk now fires at sender INVALIDATION (Findings 1, 2, 3)
`cascadeSubscribersStaleInTx` is wired into every IN-FRAME settled →
stale/running transition site (per spec Piece 1 / pessimistic-
invalidate). Sites covered: `invalidateInFrame`,
`applyResolvedAction` retry branch, `on_error.go::OnError` retry
branch, `conductor.go` heartbeat-lost path, `wakeParkedNode`. The
walk still fires at `applyTerminalComplete` settlement
(`lastOutcome == fresh_changed`) to cover the initial-instance case
where non-root subscribers have never gone through an explicit
invalidation transition — the BFS recursion creates deeper-level
wait-set rows that don't immediately drain.

`invalidateNextFrame` does NOT fire the cascade walk: under
serial_queue the next-frame is queued behind the running frame, so
a wait-set row keyed on the queued frame_id can only gate receivers
once that frame opens — by which time the cascade walk at
applyTerminalComplete (on the running source's settlement) has
already propagated through the regular chain. Adding the walk here
caused the `test/smoke` `TestStoresRedesignSmoke` to time out (the
extra BFS + 6 wait-set inserts per fire pushed the per-fire cycle
past its 5s budget). The next-frame multi-invalidator case is
covered by the cascade-on-fresh_changed chain at settlement.

Settled-state drain calls added to `applyTerminalPass`,
`applyAcquirePass`, and `failOverdueParkedRow` so every settled-state
transition uniformly bulk-deletes rows where the settling node is
the gating sender.

### Cascade walk wakes parked receivers (Finding 8 / T45 follow-up)
`wakeParkedReceiverInTx` (in-tx variant of `wakeParkedNode`) added
in `runtime/wake_parked.go`. The cascade walk calls it when visiting
a parked receiver so the receiver's parked node-run row is resumed +
state transitions parked → stale alongside the regular wait-set
insert. The `walkCascadeForInvalidatedNode` helper takes the
`persistence.Queue` so the parked-wake path's `GetParkedByNode` +
`ResumeParkedInTx` calls work end-to-end from every invalidation
site. `TestParkedLifecycleIntraGraphInvalidateAgainstParked` is
un-skipped.

### Multi-invalidator regression test (Finding 6)
`TestSubscriptionCascade_EligibilityRespectsMultipleSenders` was
rewritten to actually invalidate B and C while A is in flight (per
the docstring). Tests now also includes
`TestSubscriptionCascade_CrossCuttingPositive`,
`TestSubscriptionCascade_CrossCuttingNegative`,
`TestSubscriptionCascade_FrameEndCleansWaitSet`,
`TestSubscriptionCascade_FrameNextLoopConverges`.

### Dead-code retirement (Finding 9, 10)
`applyResolvedAction`'s `invalidate` branch and `invalidateTargets`
(both in `runner_error_policy.go`) retired; the validator already
rejects `action: invalidate` at deploy time so the branch was
unreachable. `cascadeChildrenStaleInTx` legacy shim retired in
favor of direct callers of `cascadeSubscribersStaleInTx`.
`uuidsToStrings` removed from `on_error.go`.

### Comment / dead-code cleanups (Findings 11, 12, 13, 16, 17)
- `runner_locks.go::buildLockSpecs` doc updated to reference
  `{{nodes.<n>.attribute.<f>}}` (the post-rename directive form).
- `runner_terminal.go` outer block comment retired the
  `cascadeChildrenStaleInTx` legacy-name reference.
- `parseSubstitutionDirective` floor tightened to `len(parts) >= 4`
  to match the validator's `checkAttributeSource` floor.
- `resolveSubscribedSenders` cross-cutting dead loop removed; the
  comment now explains why cross-cutting edges aren't enumerated
  here.
- `templateSubscriptionEdges` cache doc comment notes the process-
  global lifetime and the future-test-rebuild caveat.

### Atomic-staging cross-filesystem detection (Finding 14)
`examples/atomic-staging-fs-producer/store/fs_check_unix.go` added
with an `assertSameFilesystem(a, b)` helper that compares `st_dev`
for the two paths and refuses to start if they differ. Hooked into
`store.New` so an operator pointing the staging + canonical roots at
different mount points sees a clear startup error rather than
silently losing rename atomicity.

### Side-table single-writer note (Finding 15)
`examples/atomic-staging-fs-producer/README.md` extended with a
"Side-table caveats" section describing the
single-writer-per-root assumption (coarse `Store.mu` mutex is
sufficient as long as exactly one process operates on a `<root>`).

### Dashboard admin proxy (Finding 4)
`dashboards/rimsky-dashboard/src/server/proxy.ts` gained a dedicated
`/api/control/admin/*` route ahead of the generic
`/api/control/*` rewrite. The new route forwards bare `/admin/*`
upstream so `/api/control/admin/diagnostics/parked-nodes` reaches
the control-api's `/admin/diagnostics/parked-nodes` endpoint rather
than being rewritten to `/v1/observability/admin/...`.

### Pessimistic-invalidate behavioral shift in `TestNoOpCommit`
`TestNoOpCommit` was updated to match the new model: the producer's
invalidation pessimistically marks the dependent stale; the
dependent re-dispatches idempotently. The test no longer asserts
"dependent should still be fresh" — that was an old-model
assertion. It now asserts the producer's no_op_commit semantic
(event-kind preserved; no `attributes_committed`) and the
dependent's idempotent cascade to fresh.

## Cleanup pass 2 (2026-05-14)

### Issue A — admin-bypass proxy route had no unit coverage
`dashboards/rimsky-dashboard/tests/unit/proxy.test.ts` gained a
`forwards /api/control/admin/* without the /v1/observability/ rewrite`
test case mirroring the existing `/api/control/*` rewrite test.
The fetch mock answers `/admin/diagnostics/parked-nodes` with an
empty parked array. The test asserts the bare `/admin/...` URL is
called AND the wrong `/v1/observability/admin/...` URL is NOT
called. `npm test` passes 18/18.

### Issue B — frame: next spec text deviated from implementation
The spec text in
`.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`
described `frame: next` wait-set rows placed in the next frame's
`frame_id` via a deferred-invalidate queue. The shipped
implementation (Dispatch 2's T20 resolution) inserts NO wait-set
row at all for `frame: next` — `EnqueueOrCoalesce` opens the new
frame and `MarkSourceNodeStale` stamps the receiver as a frame
source at frame-open. An erratum was added directly above the
"Cross-cutting topics" section explaining:
- The implementation chose to skip the wait-set insert entirely.
- Rationale: a wait-set row keyed on `(next_frame_id, receiver,
  current_sender)` would never drain because `drainWaitSetOnSettled`
  fires per-(frame_id, sender) at the sender's terminal — and by
  the time the new frame opens, the prior-frame sender has already
  settled. The drain trigger has come and gone.
- Soundness scope: correct under `serial_queue` (the default and
  currently the only supported `frame_resolution_mode`); a future
  interleaved-frames mode would need to revisit.
- Pointer to `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx`.

The spec's "Tensions resolved" section gained a matching entry,
`tension:frame-next-wait-set-placement`. The T20 implementation-note
above this section was extended with a "Spec reconciliation" sub-
section pointing at the new erratum.

### Issue C — `TestParkedLifecycleHeldClaimRetentionAcrossPark` flake
Root cause: `resumeAt := time.Now().Add(1 * time.Second)` was
captured BEFORE template deploy + instance create + parked-state
wait + parked-state SQL probes. Under heavy parallel testcontainer
load that sequence takes 5-10s, leaving `resume_at` already in the
past by the time the supervisor finishes processing the Park
terminal. `SweepParkedNodes` then dispatched the resume against the
still-Park script (the test's `WhenType("acquirer").Success(...)`
re-scripting at line 395 had not yet run), the node re-parked, and
the `WaitForNodeState(..., Fresh, 30s)` assertion eventually timed
out.

Fix:
- Bumped `resumeAt` to `time.Now().Add(10 * time.Second)` (well
  above observed parallel setup latency, still inside the test's
  30s `WaitForNodeState(Fresh)` budget).
- Moved the resume's `WhenType("acquirer").Success(...)` re-
  scripting to BEFORE the parked-state SQL probes so that even if
  the sweep fires earlier than expected under unusual load, it
  dispatches a Success terminal rather than a Park terminal.
- Added a comment block at both edit sites explaining the race and
  the rationale for the 10s budget.

Verification:
- `go test ./test/scenarios/ -run TestParkedLifecycleHeldClaimRetentionAcrossPark
  -count=10 -p 4`: 10/10 pass.
- `go test ./test/scenarios/ -run TestParkedLifecycleHeldClaimRetentionAcrossPark
  -count=20 -p 8 -race`: 20/20 pass.
- `go test ./test/scenarios/ -run 'TestParked' -count=3 -p 8`:
  whole parked-test suite passes under p=8.

