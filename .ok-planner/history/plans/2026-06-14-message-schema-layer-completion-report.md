# Message schema layer — completion audit

**Plan:** `.ok-planner/plans/2026-06-14-message-schema-layer.md`
**Spec:** `.ok-planner/specs/2026-06-14-message-schema-layer-design.md`
**Date:** 2026-06-14

## 1. Proof walkthrough

### STORY-message-schema — declared types stale-mark subscribed receivers; undeclared types refuse loud

- **Artifact:** `test/scenarios/story_message_schema_e2e_test.go`
- **Exhibits:** Boots a real rimsky stack via `scenario.Start`; deploys a template with a two-entry `messages:` registry (`ping/recheck`, `flush/cache`); subscribes a receiver to the message-virtual-node. Leg A POSTs an undeclared type and asserts HTTP 400 with `{"error":"unknown message type","type":"...","declared_types":[...]}` plus a persistence check that no `rimsky_messages` row landed. Leg B POSTs the declared `ping/recheck` type, waits for the receiver to reach `terminal/success` through the cascade, then asserts the new frame's `triggering_message_id` matches the inserted envelope and its joined `message_type` equals `ping/recheck`.
- **Invocation:** `go test ./test/scenarios -run TestStoryMessageSchema_DeclaredAndUndeclaredTypes -count=1 -v`
- **Status:** EXHIBITS WORKING.

### STORY-cascade-send — emit-node dispatches, body carries substituted values, schema mismatch rejects at registration

- **Artifact:** `test/scenarios/story_cascade_emit_e2e_test.go`
- **Exhibits:** Deploys a template with an `executor: stub` producer (`pong`) and a downstream `emits_message: ping/recheck` emit-node whose attributes pull from `{{nodes.pong.attribute.status}}`. Drives a wake-up message; asserts (a) the producer's `terminal/success`, (b) a new envelope appears in `rimsky_messages` with the substituted `status` field, (c) the next frame opens carrying `triggering_message_id` = the cascade-emitted envelope's id, and (d) a separate sub-test that registers a template whose emit-node declares a body field the destination `body_schema` lacks gets rejected at registration.
- **Invocation:** `go test ./test/scenarios -run TestStoryCascadeEmit -count=1 -v`
- **Status:** EXHIBITS WORKING.

### STORY-cross-frame-coupling — back-edge cycle + self-drain converge; demo walks an operator through it

- **Artifacts (executable):** `test/scenarios/story_cross_frame_coupling_e2e_test.go`
- **Artifacts (demo):** `examples/cross-frame-coupling-demo.sh` + `examples/cross-frame-coupling-demo-template.yaml`, driven from `lib/services/test/scenarios/cross_frame_coupling_demo_e2e_test.go`
- **Exhibits:** The executable file pins two scenarios — a 2-cycle A → B → E (emit) → message → A reading B's data via `{{messages.<type>.<field>}}`, and a self-emit drain converging when the body's `changed` field flips false. The demo runs the same template against `rimsky-all-in-one` plus the bundled `verifier-shape-checks` executor; the shell script self-checks that both the wake frame (operator origin) and the iterate frame (instance origin) appear; the wrapping test asserts `exit 0` and also reaches into the API to confirm the cascade-send envelope landed with `sender_kind=instance`.
- **Invocation:**
  - `go test ./test/scenarios -run TestStoryCrossFrameCoupling -count=1 -v`
  - `go test ./lib/services/test/scenarios -run TestCrossFrameCouplingDemo_RunExitsZero -count=1 -v`
- **Status:** EXHIBITS WORKING.

### STORY-one-message-per-frame — N messages produce N distinct single-message frames

- **Artifact:** `test/scenarios/story_one_message_per_frame_e2e_test.go`
- **Exhibits:** POSTs N typed messages of the declared type to the same instance within one outer tick, polls the cascade-graph endpoint until every frame settles, then asserts: exactly N frame rows for the instance, every `triggering_message_id` distinct, each frame's message-ledger join carries one row, and each subscribed receiver ran exactly once per frame.
- **Invocation:** `go test ./test/scenarios -run TestStoryOneMessagePerFrame_NMessagesProduceNDistinctFrames -count=1 -v`
- **Status:** EXHIBITS WORKING.

### STORY-frame-origin-audit — every frame carries a triggering message visible through the observability surface

- **Artifacts:** `examples/frame-origin-audit-demo.sh` + `examples/frame-origin-audit-demo-template.yaml`, driven from `lib/services/test/scenarios/frame_origin_audit_demo_e2e_test.go`
- **Exhibits:** The shipped demo boots a real all-in-one stack, registers a template that exercises both operator-posted and cascade-emitted origins, polls `GET /instances/{id}/frames`, prints one line per frame with `frame_id` / `triggering_message_id` / joined envelope `type` / `sender`, and self-checks (a) every frame line has a non-empty `triggering_message_id`, (b) both `sender_kind=operator` and `sender_kind=instance` origins appear. The wrapping Go test asserts the script exits zero.
- **Invocation:** `go test ./lib/services/test/scenarios -run TestFrameOriginAuditDemo_RunExitsZero -count=1 -v`
- **Status:** EXHIBITS WORKING.

### STORY-typed-message-substitution — typo'd refs reject in both directions; runtime resolves correctly; one resolver services both surfaces

- **Artifact:** `test/scenarios/story_typed_message_substitution_e2e_test.go` + extended `lib/graph/attribute/substitution_test.go::TestSubstitution_SharedResolverServicesNodesAndMessages`
- **Exhibits:** Sub-test 1 registers a template whose receiver attribute reads `{{messages.ping/recheck.not_a_real_field}}` and asserts registration rejects naming the missing field. Sub-test 2 registers an emit-node whose `attributes:` declares a body field the destination `body_schema` lacks and asserts rejection. Sub-test 3 drives a working back-edge cycle and confirms the receiver reads the sender's data through `{{messages.<type>.<field>}}`. The unit-level test asserts both `{{nodes.X.attribute.Y}}` and `{{messages.X.Y}}` flow through the same resolver function.
- **Invocation:** `go test ./test/scenarios -run TestStoryTypedMessageSubstitution -count=1 -v && go test ./lib/graph/attribute -run TestSubstitution_SharedResolverServicesNodesAndMessages -count=1 -v`
- **Status:** EXHIBITS WORKING.

### STORY-debug-channel — override accepted on paused + breakpoint; refused on healthy; permission-gated

- **Artifact:** `test/scenarios/story_debug_channel_e2e_test.go`
- **Exhibits:** Boots a real stack and drives every gate transition through the real control-API: Leg 1 confirms a healthy instance rejects `POST /debug/override` with HTTP 409 and predicate names; Leg 2 pauses the instance, runs `invalidate_node`, asserts the node-run state flips to `stale`, an audit-event row of kind `debug.override.applied` is appended, and clearing the gate lets the cascade re-dispatch; Leg 3 installs a pause-mode breakpoint, runs `set_attribute` while a runner is parked, asserts the operator-supplied attribute lands on the next dispatch's `ExecuteRequest`; Leg 4 confirms a key lacking `instance:debug-override` gets HTTP 403.
- **Invocation:** `go test ./test/scenarios -run TestStoryDebugChannel_GateAndOverrideAcrossRealStack -count=1 -v`
- **Status:** EXHIBITS WORKING.

## 2. Technical decisions kept

### TD-send-as-node-kind — cascade-driven emission lives on a dedicated node-kind declared by `emits_message:`

- Field on the template DSL: `lib/foundation/spec/template.go:229` adds `EmitsMessage string` to `TemplateNodeDef`.
- Mutual-exclusion validation (`executor` / `delegate` / `emits_message` exactly one): `lib/graph/node/template_validator.go` validator pass on the node definitions.
- Dispatch path: `lib/runtime/runner_emit_message.go::emitCascadeMessageInTx`, called from the terminal-resolution flow when the node's dispatch is `emits_message`.

### TD-attribute-set-as-body — emit-node `attributes:` is the body; exact shape match required

- Body construction: `lib/runtime/runner_emit_message.go:161` marshals the resolved attribute set directly as the envelope payload (no mapping layer).
- Exact-shape validation against the destination `body_schema`: `lib/graph/node/template_validator.go` `validateEmitsMessage` pass.

### TD-single-frame-creation-path — frames open only at the message-delivery boundary

- One frame producer: `lib/graph/frame/producer.go::EnqueueFrame` is the sole entry point; `EnqueueOrCoalesce` is gone.
- Call sites: `lib/runtime/cascade_invalidate.go` (operator/synthetic), `lib/runtime/runner_emit_message.go:214` (cascade-send), `lib/runtime/synthetic_envelope.go:117` (wake-park synthetic envelopes), `lib/graph/scheduler/scheduler.go:170` (pure-cascade sweep). None live inside the cascade walker.
- The cascade walker (`lib/runtime/runner_terminal.go`) carries no `frame.EnqueueFrame` call; the prior `case node.FrameNext:` arm is deleted.

### TD-debug-channel-gate-paused-or-breakpoint — legal iff paused or pause-mode breakpoint hit

- Gate enforcement inside the request tx: `lib/control/controlapi/debug_override.go:142-216` reads `paused` + the unresumed-breakpoint-hit predicate in the same tx as the mutation; returns HTTP 409 with `{"states":["paused","breakpoint"]}` when neither holds.
- Route + permission gate: `lib/control/controlapi/debug_override.go:78` registers `POST /instances/{id}/debug/override` under `instance:debug-override`; action listed at `lib/control/controlapi/actions.go:326`.

### TD-envelope-type-discriminator — `kind` → `type`; `message_kind` → `message_type`

- Migration: `lib/foundation/persistence/postgres/migrations/010-message-schema-layer.sql:128` `RENAME COLUMN kind TO type`; line 136 `RENAME COLUMN message_kind TO message_type`. SQLite mirror does the same via the rebuild dance (`lib/foundation/persistence/sqlite/migrations/010-message-schema-layer.sql:171,222`).
- Go structs: `lib/foundation/persistence/messages.go:26` `Type string`; `lib/foundation/persistence/publisher_subscriptions.go:47` `MessageType string`.
- Control-API request shape: `lib/control/controlapi/messages.go` `PostInstanceMessagesRequest.Type` (JSON tag `type`).

### TD-one-message-per-frame — every frame carries at most one delivered message

- Delivery picks the oldest single pending message: `lib/runtime/message_delivery.go:495` `oldest := &pending[0]`; comment at line 478-480 names the property.
- SQL gate: `lib/foundation/persistence/postgres/messages.go:82` and `lib/foundation/persistence/sqlite/messages.go:78` carry `LIMIT 1` on `listPendingForInstanceSQL`.

### TD-pre-v1-pure-removal — retired surfaces leave no code traces

- `frame_resolution_mode` / `frame_delivery_mode`: dropped from migrations, structs, and request bodies in pass 1-2. No detection rule; `controlapi/templates.go` `DisallowUnknownFields()` rejects through the generic JSON decoder.
- `message/*` taxonomy entry removed at `lib/foundation/signal/taxonomy.go:21-26` with a comment explaining the unknown-type-path validator now handles the rejection.
- Backfill subsystem removed: `lib/control/controlapi/backfills.go`, `cmd/rimsky/cli/backfill.go`, `lib/runtime/backfill.go`, `test/scenarios/backfill/`, and the dedicated backfill scenarios are all deleted (visible in `git status`).
- `frame:` modifier (`FrameIn`/`FrameNext`): no remaining references in `lib/` (only `RebindRunFrameInTx`, an unrelated symbol).

## 3. Technical decisions diverged

### TD-send-as-node-kind — idempotency-key shape

- **Spec said:** Envelope's `Idempotency-Key` deterministic on the dispatching node-run's `node_run_id` (spec §Components, plan task 32).
- **Implemented:** `cascade-send:<node_id>:<frame_id>` (the emit-node's static UUID plus the frame id), per `lib/runtime/runner_emit_message.go:166`.
- **Flavor:** improved.
- **Reason:** Interim-review finding 9 surfaced that `node_run_id` is regenerated on every fresh stale-mark / supervisor hard-failure re-enqueue, so the spec's shape would have duplicated envelopes on every infra retry. `(node_id, frame_id)` is stable across re-enqueue and collapses the retry onto the same dedup row. The load-bearing property (one envelope per logical emit even on retry) is satisfied by this shape; the spec's shape would have failed it.

### TD-debug-channel-gate-paused-or-breakpoint — permission-action segment count

- **Spec said:** Working name `instance:debug:override` (three-segment).
- **Implemented:** `instance:debug-override` (two-segment), at `lib/control/controlapi/actions.go:326`.
- **Flavor:** selected.
- **Reason:** Spec marked the name as "working name"; plan Task 37 resolved it to two segments to match the prevailing `instance:*` action grammar in the existing slice (`registeredActions`).

### Additional migration — `011-waitset-topic-kind-drop-message.sql`

- **Spec said:** The plan's Task 20 said the wait-set `topic_kind` schema change "if a constraint exists, write a migration OR add it to the Pass 1 migration."
- **Implemented:** A separate migration file `011-waitset-topic-kind-drop-message.sql` in both Postgres and SQLite, distinct from `010`.
- **Flavor:** selected.
- **Reason:** Plan left the placement open (separate vs folded). Implementer chose a separate migration for clarity; both files apply cleanly through the migration runner.

### Synthetic-envelope frame-creation helper (necessitated)

- **Spec said:** "A frame opens only when a message lands in the ledger" (single creation path). The spec didn't explicitly name the wake-park / cron / parked-resume code paths that need to open a frame against the same constraint.
- **Implemented:** `lib/runtime/synthetic_envelope.go` constructs a synthetic-but-real message envelope and calls `frame.EnqueueFrame(...)` for runtime-internal wake paths (parked resume, cron tick, asset-materialize).
- **Flavor:** necessitated.
- **Reason:** The "every frame has a triggering message" invariant requires that every internal wake path also produce a message envelope. The synthetic-envelope helper is the necessary closure for `STORY-frame-origin-audit`'s acceptance to hold across all paths, including non-operator wake-up paths the spec did not enumerate.

### `advanceOneFrame` parked-aware wake (necessitated)

- **Spec said:** Did not name a parked-wake parity branch.
- **Implemented:** `lib/graph/frame/engine.go`'s `advanceOneFrame` wake-id loop branches on the resolved node's state and calls `wakeParkedReceiverWithDepsInTx` for parked rows, mirroring `cascadeMessageVirtualNodeSettleInTx`'s parked-vs-running split.
- **Flavor:** necessitated.
- **Reason:** Interim-review finding 1 surfaced that `MarkStaleForCascade` against a parked row produces the inconsistent `(phase=parked, state=stale)` shape, so the wake silently no-ops — operator invalidate / asset-materialize against a parked node would never resume. The branch is necessary for the synthetic-envelope wake (and operator invalidate) to honor STORY-frame-origin-audit's acceptance on parked targets.

### MCP `message_send` descriptor refresh (necessitated)

- **Spec said:** Did not name the MCP route descriptor surface.
- **Implemented:** The MCP tool descriptor at `lib/control/controlapi/mcp_route.go` was updated to advertise `type` (not `kind`) and drop `target` (interim-review finding 2 confirmed earlier-state was stale).
- **Flavor:** necessitated.
- **Reason:** The HTTP handler's wire shape changed; the MCP descriptor is the schema LLM-driven clients read to construct requests. Without the refresh the universal `/instances/{id}/messages` endpoint would be unreachable via MCP — STORY-message-schema's acceptance would fail through that surface.

### `mutated` counter for debug-override audit (necessitated)

- **Spec said:** Audit row records the override's effect; did not name the counting semantics.
- **Implemented:** `lib/control/controlapi/debug_override.go::applyDebugOverride` increments only when stale-mark or attribute-write actually persisted (per interim-review finding 4 fix).
- **Flavor:** necessitated.
- **Reason:** Without the guard, the HTTP response and audit row claimed work that did not happen on no-op overrides, mis-pinning STORY-debug-channel's acceptance ("the override actually mutates the graph"). The guarded counter is necessary for the proof to match the audit trail.

## 4. Post-walk corrections

Three corrections to the report state landed after the initial completion-audit pass during the post-walk divergence review.

### Cross-stack publisher / sensor proofs restored (post-walk correction)

- **Earlier (incorrect) entry:** The original report carried a "Cross-cutting publisher / sensor proof gap (necessitated consistent with spec)" divergence entry asserting the spec directed retiring the cross-stack proofs and the deletion was consistent with the spec.
- **Actual state:** The 5 cross-stack proofs (`lib/services/test/scenarios/sensor_cascade_e2e_test.go`, `sensor_cron_restart_recovery_e2e_test.go`, `sensor_http_e2e_test.go`, `sensor_object_store_e2e_test.go`, `sensor_webhook_e2e_test.go`, plus `examples/publisher/main_e2e_test.go`) were RESTORED and patched mid-walk per user direction. The gap is closed.
- **Disposition:** The original divergence entry has been removed from Section 3 to reflect the corrected state.

### `advanceOneFrame` parked-aware wake — backed out (post-walk correction)

- **Originally implemented (per Section 3 entry above):** `lib/graph/frame/engine.go::advanceOneFrame` branched on parked-vs-running for the wake-id loop, calling `wakeParkedReceiverWithDepsInTx` for parked targets.
- **Backed out post-walk (item 6 of the divergence walk):** The branch was guarding an unreachable state under intra-frame park semantics — the parked-vs-stale shape `MarkStaleForCascade` rejects cannot arise inside a single frame's promotion tx because the wake-id list is computed pre-promotion. The dead-code branch and its helper `wakeParkedReceiverInTx` (in `lib/runtime/wake_parked.go`) were removed via a separate fixer. The cascade-walk parked-vs-running split in `lib/runtime/message_delivery.go::wakeParkedReceiverWithDepsInTx` survives — that path operates on inter-frame state where parked rows are still reachable.
- **Disposition:** The Section 3 "`advanceOneFrame` parked-aware wake" entry describes the originally-implemented divergence; this section records that it has been backed out. The frame-engine promotion path no longer carries parked-aware branching.

### Operator-invalidate route retired (post-walk correction)

- **Originally implemented:** The `POST /v1/nodes/{id}/invalidate` route and its full stack (admin sibling, CLI subcommand, runtime `InvalidateNode` chain, `InvalidateAdapter`, `UnifiedInvalidate`, `node:invalidate` permission action, MCP `node_invalidate` descriptor) shipped with the message-schema-layer reshape.
- **Retired post-walk (item 9 of the divergence walk):** The route and its dependents were deleted on the premise that operator-driven node invalidation now flows through `POST /instances/{id}/debug/override` (the gated debug-channel surface) for the debuggable lifecycle states, and `POST /v1/nodes/{id}/reset` for failed nodes. The retirement removed code at `lib/control/controlapi/nodes.go` (route + handler), `cmd/rimsky/cli/invalidate.go` (CLI verb), `lib/runtime/` (`InvalidateNode` chain, `InvalidateAdapter`, `UnifiedInvalidate`), the `node:invalidate` entry in `lib/control/controlapi/actions.go`, and the `node_invalidate` descriptor in `lib/control/controlapi/mcp_route.go`.
- **Surfaced loose ends (closed in this same post-walk pass):** `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go` was deleted (its message-activity interleaving leg depended specifically on the retired route's `message_emitted` emission during a paused window — no equivalent emit site exists in the current code), and `lib/services/test/scenarios/mcp_transport_parity_e2e_test.go` had a new `instance_lifecycle` category added (with `instance_pause` mutation) to replace the write-side parity coverage the retired `node_invalidate` leg held.
- **Design-doc drift closed inline:** Design-corpus references to the retired operator-invalidate surface were updated in place to reflect the current code state — `concepts/role-template.md` (`agent-supervisor` role drops `node:invalidate` from its grants), `stories/node-admin.md` (story rewritten to "inspect and reset"; the operator-invalidate capability moves to typed-message and debug-channel paths), `concepts/asset.md` (materialize-invariant rewritten to drop the `invalidate-kind message` framing), and `stories.md` TOC (node-admin summary line). Whether the `agent-supervisor` bundled role should also gain `instance:debug-override` is a discretionary forward-looking question for a future design discussion.

### Forward-reference: deferred architectural questions

Two architectural questions surfaced during the walk but were intentionally deferred to a separate design pass — they're captured in `sketch:2026-06-15-instance-creation-and-empty-message-trigger.md`:

- **Instance creation and root auto-trigger.** Today instance creation auto-synthesizes an `instance/root` envelope that opens the first frame and stale-marks every structural root. The deferred shape would keep instance-creation as a pure row insert and require an explicit message (possibly empty-typed) to invoke work.
- **`asset/materialize` and `node/reset` frame-creation shape.** Today both runtime-synthetic envelopes ride the same synthesis path the operator route used to use. Whether they should retain that shape or move to per-message-type author declaration (mirroring the instance/root question) is its own design walk.

## Coverage check

- **Stories exhibited:** 7 / 7 in manifest. No GAPs.
- **Technical decisions accounted for:** 7 / 7 in manifest (7 kept + 2 diverged; TD-send-as-node-kind appears in both columns — kept as a kind, diverged on idempotency-key shape; TD-debug-channel-gate-paused-or-breakpoint appears in both — kept on the gate predicate, diverged on the action-name segment count). All seven TDs are explicitly enumerated. No silent attestations.
- **Necessitated work surfaced:** 4 active items (synthetic-envelope helper, MCP descriptor refresh, debug-override `mutated` guard, separate migration 011) — flagged with rationale. A fifth item (parked-aware `advanceOneFrame` wake) was originally listed but has been backed out per Section 4; the entry survives in Section 3 with the backout recorded in Section 4.
- **Process defects:** none. The publisher / sensor cross-stack proof gap originally surfaced as a divergence was corrected mid-walk (proofs RESTORED, see Section 4) and the misleading divergence entry has been removed. The interim-review findings (sketch `2026-06-14-interim-review-findings.md`) were resolved during execution and the divergences they motivated are accounted for above.
