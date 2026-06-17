# Empty-message wake trigger and synthetic-envelope retirement — Completion report

Spec: `.ok-planner/specs/2026-06-15-empty-message-wake-trigger-design.md`
Plan: `.ok-planner/plans/2026-06-15-empty-message-wake-trigger.md`

## 1. Proof walkthrough

### STORY-instance-create-is-idle — operator creates an instance that does nothing on its own
- **Proof artifact:** `test/scenarios/instance_create_is_idle/instance_create_is_idle_e2e_test.go`
- **Exhibits:** end-to-end through the in-process scheduler + supervisor + control-api stack against a Postgres testcontainer. The test bypasses `Harness.CreateInstance` (which now emits an internal wake) and drives `POST /v1/instances` directly so it can observe the idle state — empty frame collection, empty message ledger, no node-runs, and exactly one `OnInstanceCreated` lifecycle-subscriber callback.
- **Invocation:** `go test ./test/scenarios/instance_create_is_idle/... -count=1`
- **Annotation:** carries `@story: instance-create-is-idle` at line 23.
- **Status:** EXHIBITS WORKING.

### STORY-empty-message-wakes-roots — empty message wakes every structural root via the universal message path
- **Proof artifact:** `test/scenarios/empty_message_wake/empty_message_wakes_roots_e2e_test.go`
- **Exhibits:** end-to-end on the all-in-one stack: deploys a template with two structural roots (`root1`, `root2`), one direct downstream (`down`), one cross-cutting watcher (`watch`); creates the instance via raw HTTP (asserts idle); posts an empty-bodied message (`type: ""`) with an Idempotency-Key (asserts `201 Created`); observes exactly one frame whose `triggering_message_id` matches the returned `message_id`; observes node-runs for both structural roots and their downstream subscriber; replays the same key (asserts `200 OK`, original `message_id` returned, no second frame).
- **Invocation:** `go test ./test/scenarios/empty_message_wake/... -count=1`
- **Annotation:** carries `@story: empty-message-wakes-roots` at line 35.
- **Status:** EXHIBITS WORKING.

## 2. Technical decisions kept

### TD-empty-message-as-root-trigger
Implicit `""` entry seeded at template registration into the declared-types set; author-declared empty-type entries refused as reserved-for-runtime.
- **Embodied at** `lib/runtime/subscription_loaders.go:145` (`set[""] = struct{}{}` in `declaredMessageTypesForTemplate`) and `lib/graph/node/template_validator.go:3134` (reserved-for-runtime rejection in `validateMessages`).
- **Carried at receipt** by `lib/control/controlapi/messages.go:317-319` (admit branch when `body.Type == ""`) and the registry-mismatch response at `lib/control/controlapi/messages.go:460-465` which advertises the implicit type alongside declared types.

### TD-structural-root-edge-injection-at-registration
`BuildSubscriptionEdges` injects one edge per structural root receiver keyed by sender=`""` with `WakeOnChange: true`, `ForceUpstreamRefresh: false`, `SenderBoundToEmpty: true`, type-pattern `terminal/success`.
- **Embodied at** `lib/graph/node/subscription_edges.go:595-660` (the structural-root augmentation pass at the tail of `BuildSubscriptionEdges`; sub-graph-internal nodes are excluded, `messages.<type>.<field>` refs disqualify a node from root status alongside explicit `subscribes:` entries).

### TD-empty-sender-key-edge-disambiguation
`SenderBoundToEmpty bool` flag on `SubscriptionEdge` separates structural-root edges from cross-cutting edges under sender=`""`.
- **Embodied at** `lib/graph/node/subscription_edges.go:76-85` (field definition) and applied at `lib/graph/node/subscription_edges.go:252` (`Match` filter via `senderBoundFilter`), `:341` (`ReceiverNodeTypesForSender` filter), `:376` (`ReceiverEdgesForSender` filter), `:472` (`AllEdges` filter).

### TD-node-reset-as-pure-retry-budget-clear
`POST /nodes/{id}/reset` preserves the failed-state gate and clears retry budget; no envelope synthesis, no frame opens.
- **Embodied at** `lib/control/controlapi/nodes.go:174-264` (`handleResetNode`): state gate at `:194-200` (`409 Conflict` for non-failed), retry-budget clears at `:217-233` (`UpdateError`, `ResetFailedTerminalSettlingSignalType`, `SetFrameID(nil)`), `@decision: node-reset-as-pure-retry-budget-clear` at `:239`, audit Append at `:251-260`. No `EnqueueSyntheticWakeFrame` call.

### TD-asset-materialize-endpoint-retired
Handler, route, CLI, MCP tool, action row all retired.
- **Embodied by absence:** `grep -rn 'handleAssetMaterialize|RunAssetMaterialize|materializeRequest|MaterializeAsset|"asset_materialize"|"asset:materialize"' lib/ cmd/` returns zero matches. Diff confirms `cmd/rimsky/cli/asset.go`, `cmd/rimsky/cli/client.go`, `lib/control/controlapi/assets.go`, `lib/control/controlapi/mcp_route.go`, and `lib/control/controlapi/actions.go` all modified to remove the surface.

### TD-synthetic-envelope-mechanism-retired
The whole synthetic-envelope machinery retires: file, payload fields, reserved-field check, reserved-property check, frame-engine reader.
- **Embodied by absence:**
  - `lib/runtime/synthetic_envelope.go` deleted (`git status` lists it under `deleted:`).
  - `grep -rn 'EnqueueSyntheticWakeFrame|expandWakeWithUpstreamRefresh' lib/ cmd/ test/` returns zero matches.
  - `grep -rn 'wake_node_ids|wait_set_pairs|errPayloadCarriesReservedField|reservedPayloadFieldWakeNodeIDs' lib/ cmd/ test/` returns zero matches.
  - `grep -rn '@blessed-invariant.*upstream-staled-before-receiver-dispatch'` returns zero matches.

### TD-test-harness-invalidate-node-retired
`Harness.InvalidateNode` deleted; 2 surface-tests retired; remaining scaffolding + story-proof call sites reinstrumented with typed-message wakes.
- **Embodied by absence + reinstrumentation:**
  - `grep -n 'func (h \*Harness) InvalidateNode' test/support/scenario/harness.go` returns zero matches.
  - `TestOperatorInvalidateTargetOnly` and `TestParkedLifecycleResumeOnExternalInvalidate` no longer exist (`grep` returns zero matches).
  - Typed-message reinstrumentation present at e.g. `test/scenarios/no_op_commit_test.go:50,56,93,134`, `test/scenarios/cascade_invalidate_test.go:49,55,100,118`, `test/scenarios/multi_hard_dep_test.go:66,72,147,178`, `test/scenarios/explicit_attribute_context_read_test.go:95,96,104,142,215,246,287`, `test/scenarios/all_upstream_gating_test.go:60`, `test/scenarios/per_run_attributes/hard_dep_test.go:68` (test-wake message + subscribes + `PostInstanceMessage`).

### TD-test-harness-create-instance-wakes-roots-after-create
`Harness.CreateInstance` / `CreateInstanceWithOverrides` gain an internal empty-message wake step after the create POST.
- **Embodied at** `test/support/scenario/harness.go:584` (`PostInstanceMessage(id, "", nil, "harness-wake-create-"+id.String())` inside the wake-after-create branch) and `:724` (the auth-aware sibling path).

### TD-compose-driver-emits-empty-message-after-create
Compose driver emits one empty message per declared instance after create with a deterministic Idempotency-Key derived from the instance key.
- **Embodied at** `cmd/rimsky/cli/compose/run.go:324-326` (`wakeKey := "compose-wake-" + ci.Key`; `c.CreateInstanceMessage(bootCtx, ci.ID, wakeKey, cli.CreateInstanceMessageRequest{})`).

## 3. Technical decisions diverged

No technical decisions diverged. Every TD in the spec manifest is honored as specified.

Three implementation choices the spec did not anticipate, surfaced during execution:

- **`PostInstanceMessageWithAuth` sibling helper** (`test/support/scenario/harness.go:601-650`). Spec Task 23 specified one `PostInstanceMessage` helper; implementation added an auth-aware variant so the create-time wake honors api-key bearer auth when the harness is configured for it. Flavor: **necessitated** (the harness's `CreateInstanceWithServiceBindings` path runs under api-key auth, so the internal wake step needed an auth-carrying variant). The base `PostInstanceMessage` delegates to `PostInstanceMessageWithAuth` with an empty bearer — same uniform code path under the hood.
- **`harness-wake-create-<instance_id>` Idempotency-Key prefix** rather than the spec's `harness-wake-<instance_id>` shape. Flavor: **improved**. The longer prefix discriminates the harness's create-time wake from any in-test wake the test body might post with the natural `harness-wake-<iid>` shape so collisions are structural rather than accidental. Documented at `test/support/scenario/harness.go:510-514` and `:569-571`.
- **Parallel-helpers sweep** — fifteen test-scaffolding helpers across `cmd/`, `examples/`, and `lib/services/test/` were updated to emit empty-message wakes after instance creation, applying the same pattern as `TD-test-harness-create-instance-wakes-roots-after-create` and `TD-compose-driver-emits-empty-message-after-create`. Flavor: **necessitated** — parallel `createInstance` helpers exist throughout the codebase that bypass `Harness.CreateInstance` and the compose driver; without the same wake-after-create plumbing each call would create an instance that never dispatches, regressing every existing test built on those helpers. The TD-as-a-class covers: `cmd/rimsky/cli/run.go::RunRun`, `cmd/rimsky/cli/internal/clitest/server.go::handleCreateInstanceMessage`, `cmd/rimsky/cli/internal/clitest/state.go::RecordInstanceMessage`, `examples/claimproducer/main_e2e_test.go::createClaimInstance`, `examples/executor/main_e2e_test.go::createInstance`, `lib/services/subscribers/openlineage/subscriber_test.go::postInstance`, `lib/services/test/scenarios/atomic_staging/fs_held_swap_e2e_test.go::createHeldSwapInstance`, `lib/services/test/scenarios/claim_producer_observability_dashboard_e2e_test.go::createObsInstance`, `lib/services/test/scenarios/pg_error_classes/pg_error_classes_test.go::createInstance`, `lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go::createInstance`, `lib/services/test/scenarios/sqlite_all_in_one_test.go::createScenarioInstance`, `lib/services/test/scenarios/stores/helpers.go::createInstance`, `lib/services/test/scenarios/subscriber_openlineage_e2e_test.go::createOLInstance`, `lib/services/test/scenarios/terminate_after_run_e2e_test.go::createTerminateAfterRunInstance`, `lib/services/test/scenarios/verifier_severity_partition_e2e_test.go::createSeverityPartitionInstance`, `lib/services/test/smoke/stores_redesign_smoke_test.go::smokeCreateInstance`. Each helper now POSTs an empty-bodied message with a deterministic Idempotency-Key after the create call, matching the canonical pattern.

## 4. Coverage divergences

One coverage divergence, surfaced and remediated during review:

- **Parked-state hold-the-frame coverage was substituted with async-callback hold-the-frame coverage during the reinstrumentation of `TestParkedHoldsFrame_EndToEnd`.** The original test (pre-spec) used `Park(...)` + `Harness.InvalidateNode` to exercise "a parked node holds its frame open"; the reinstrument moved to `AwaitAsyncCallback(...)` + supervisor-callback-resolve, which is a different property (async-callback-pending holds the frame). The async-callback property is genuinely valuable but is structurally distinct from the parked-state property (the held-frames diagnostic at `route:GET /v1/admin/diagnostics/held-frames` is scoped to `phase='parked'` and does not surface async-callback-pending rows). Both properties are load-bearing for the 2026-06-03 durable-by-default lifecycle spec's Pass-1 `ListRunningFramesNoPendingNodes` semantics and Pass-3 instance-terminal guard. **Remediation:** the async-callback test was renamed to `code:test/scenarios/async_callback_holds_frame_e2e_test.go::TestAsyncCallbackHoldsFrame_EndToEnd` and a new `code:test/scenarios/parked_holds_frame_e2e_test.go::TestParkedHoldsFrame_EndToEnd` was added that exercises the parked-state property end-to-end via the typed-message wake path: a two-node template (`root` succeeds; `parker` cascades to parked); the held-frames diagnostic surfaces the parked node's frame; a typed-message wake (`test/wake/parker`) resolves the parked node; `terminate_after_run` fires only after the parked work resolves.

- **Coverage check:** every story listed in the spec manifest as having a proof was checked for `@story:<slug>` annotation coverage. STORY-instance-create-is-idle is annotated at `test/scenarios/instance_create_is_idle/instance_create_is_idle_e2e_test.go:23`, `lib/control/controlapi/instances.go:1397`, `lib/control/controlapi/app_test.go:441`, `lib/services/test/scenarios/control_api_idempotency_required_e2e_test.go:111`. STORY-empty-message-wakes-roots is annotated at `test/scenarios/empty_message_wake/empty_message_wakes_roots_e2e_test.go:35`, `lib/runtime/subscription_loaders.go:120`, `lib/graph/node/subscription_edges.go:558` and `:650`, `lib/control/controlapi/messages.go:316,459`, `lib/control/controlapi/messages_test.go:680,743`, `test/scenarios/frame_resolution/helpers_test.go:82`, `test/scenarios/pure_cascade_test.go:12`, `cmd/rimsky/cli/client.go:960`. Each touched-by-spec mutated story (asset-management, instance-lifecycle, node-admin, one-shot-to-terminal, claim-handoff-durable, explicit-attribute-context-read, upstream-pull-on-invalidate) carries `@story:<slug>` on its proof artifact.

- **Intent-drift check:** the new + mutated proofs walk the new mechanism. Each story `Proof` field describes a behavioral observation that the proof exhibits via the typed-message wake + cascade walker path (no `Harness.InvalidateNode`, no `EnqueueSyntheticWakeFrame`). Verdict per artifact: **satisfies**.

- **Annotation integrity:** `rg -n "@story:\s*\S+"` returns 141 annotations across 89 unique slugs (subtracting two false positives — `host-agent-control-plane.` with sentence-ending period and `uncovered-substitution-rejected` lines that include the annotation in tab-indented blocks — and accounting for the empty-message wake's pinned slugs). Every unique slug resolves to a live `stories/<slug>.md` file. Zero dangling annotations.

## Coverage check

- **Stories exhibited:** 2 / 2 in spec manifest. The 7 mutated/preserved stories the spec also touched (4 spec stories + 3 proof-shift stories) all carry `@story:<slug>` on the proof artifact and the proof's mechanism walks the post-spec path.
- **TDs:** 9 kept + 0 diverged = 9 / 9 in spec manifest. Three unanticipated implementation choices (auth-aware sibling helper, longer key prefix, parallel-helpers sweep) classified as `necessitated` / `improved` / `necessitated`.
- **Coverage divergences:** 1 coverage gap surfaced and remediated (parked-state hold-the-frame, restored via the rename + new parked-state test); 0 intent drifts; 0 dangling annotations.
