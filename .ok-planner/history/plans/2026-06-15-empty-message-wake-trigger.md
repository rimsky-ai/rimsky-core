# Empty-message wake trigger and synthetic-envelope retirement — Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-15-empty-message-wake-trigger-design.md`
**Goal:** Retire the runtime-synthetic-envelope mechanism by adding an implicit empty-message wake trigger to the typed-message path; narrow three operator surfaces (instance-create, asset-materialize, node-reset); and retire the test harness's `InvalidateNode` helper entirely, reinstrumenting dependent tests with legitimate triggers.
**Architecture:** The cascade walker's per-template inverse-edge map gains runtime-injected structural-root edges keyed by sender=`""`. The per-template declared-types set gains an implicit `""` entry seeded at registration. Empty-typed messages then flow through the existing `route:POST /instances/{id}/messages` path uniformly with any typed message; the cascade walker fires structural roots via the augmented map. With the new mechanism in place, the four runtime-internal type-paths (`instance/root`, `node/reset`, `asset/materialize`, `node/invalidate`) and their chokepoint helper (`code:lib/runtime/synthetic_envelope.go::EnqueueSyntheticWakeFrame`) all retire, along with the `wake_node_ids`/`wait_set_pairs` wire fields, the receipt-side reserved-field check, the registration-side reserved-property check, and the frame-engine `wake_node_ids` reader at promotion. The asset-materialize endpoint deletes outright; the node-reset endpoint trims to a pure retry-budget-clear; the test harness's `InvalidateNode` retires entirely and dependent tests reinstrument with typed-message-driven invalidation (or, for parked-state, real executor + callback).
**Tech Stack:** Go (stdlib `log/slog`, `go-chi/chi`, `jackc/pgx/v5`, `modernc.org/sqlite`). Tests use `testcontainers-go` for the all-in-one stack scenarios.

---

## Pass 1: Add the empty-message wake mechanism (foundation)

**Goal:** Introduce the augmented inverse-edge map and the implicit empty-type registry entry so the new typed-message wake path works end-to-end. The synthetic-envelope mechanism continues to exist and serve its current four callers; this pass adds the new mechanism alongside it. Tree compiles and all existing tests pass at the end of this pass.

**Scope:** Tasks 1–10

**Falsifier:** `SubscriptionEdge` has no `SenderBoundToEmpty` field, OR `BuildSubscriptionEdges` does not emit structural-root edges keyed by sender=`""` with `SenderBoundToEmpty=true`, OR the `Match` lookup at `code:lib/graph/node/subscription_edges.go::SubscriptionEdgeMap.Match` does not disambiguate structural-root edges from cross-cutting edges under the `""` sender key, OR `templateDeclaredMessageTypes` does not include the implicit `""` entry after registration, OR `code:lib/graph/node/template_validator.go` accepts an author-declared `messages:` entry of type `""`.

### Task 1: Add the `SenderBoundToEmpty` field on `SubscriptionEdge`

**Files:** `lib/graph/node/subscription_edges.go`

**Steps:**

1. In the `SubscriptionEdge` struct (around line 48), add a new field:
   ```go
   // SenderBoundToEmpty distinguishes runtime-injected structural-root
   // edges (true; fire only when the actual settling sender's type is
   // "") from cross-cutting `instance: true` subscription edges (false;
   // fire on every settled sender) — both live under the empty
   // sender-key. Author-declared subscriptions cannot set this flag;
   // the runtime owns it.
   //
   // @concept: node-subscription
   // @concept: cascade
   SenderBoundToEmpty bool
   ```
2. Run `go build ./lib/graph/node/...` and confirm it compiles.

### Task 2: Augment `BuildSubscriptionEdges` to inject structural-root edges

**Files:** `lib/graph/node/subscription_edges.go`

**Steps:**

1. Read `BuildSubscriptionEdges` in `subscription_edges.go` (its full body; locate by `func BuildSubscriptionEdges`).
2. After the existing population from `subscribes:` entries completes (i.e., just before the function returns the populated map), add a structural-root augmentation pass:
   - For each top-level template node (`spec.Spec.Nodes`, and the entry node of each non-main graph in `spec.Spec.Graphs` if those count as structural roots too — match the rules used in `code:lib/control/controlapi/instances.go::handleCreateInstance` lines 1420-1452, which excludes sub-graph internal nodes and treats a node as a root when it has no `subscribes:` entry naming an upstream `Node != ""` AND no upstream node refs from its attribute substitution).
   - For each structural root, call `m.Insert("", SubscriptionEdge{ReceiverNodeType: <node-type>, TypePattern: signal.TypePath("terminal/success"), WhenExpr: nil, SubscriptionScope: "direct", WakeOnChange: true, ForceUpstreamRefresh: false, SenderBoundToEmpty: true})`.
3. Add a top-of-function GoDoc comment (or extend the existing one) explaining the structural-root injection, citing `@decision: structural-root-edge-injection-at-registration` and `@story: empty-message-wakes-roots`.
4. Run `go build ./lib/graph/node/... && go test ./lib/graph/node/... -run TestBuild -count=1` and confirm existing tests still pass.

### Task 3: Disambiguate structural-root edges in `SubscriptionEdgeMap.Match`

**Files:** `lib/graph/node/subscription_edges.go`

**Steps:**

1. Read `SubscriptionEdgeMap.Match` (lines 158-168, plus the helper `appendMatches`).
2. Modify `appendMatches` (or `Match` itself, depending on where the filter fits cleanly) so that when looking up edges under the `""` sender-key, edges with `SenderBoundToEmpty=true` fire **only** when the calling site's `senderNodeType` parameter is `""`. Cross-cutting edges (`SenderBoundToEmpty=false`) continue to fire regardless of the calling site's `senderNodeType`.
   - Concretely: `Match` already calls `appendMatches(out, m.bySender[""], signalType)` only when `senderNodeType != ""` (line 164-165). Adjust this branch so the call still happens, but the cross-cutting matches are filtered to exclude `SenderBoundToEmpty=true` edges. Add a separate branch when `senderNodeType == ""` that calls `appendMatches` against `m.bySender[""]` and includes both flag values.
   - A clean implementation: add a `senderBoundFilter` parameter to `appendMatches`, with values `crossCuttingOnly` / `boundToEmptyOnly` / `all`. Or do the filtering inline in `Match` after collecting matches.
3. Apply the same disambiguation to `ReceiverNodeTypesForSender` (lines 189-214), `ReceiverEdgesForSender` (lines 261-275), and `AllEdges` if any of them are used during cascade walks where the same conflation could occur. `SenderNodeTypesForReceiver` (lines 233-253) deliberately excludes the empty sender-key; that stays — structural-root edges' receiver→sender mapping is not meaningful (they have no upstream).
4. Add unit tests in `subscription_edges_test.go` asserting:
   - With both a cross-cutting edge and a structural-root edge under sender=`""`, calling `Match(senderNodeType="executor-foo", signalType="terminal/success")` returns the cross-cutting edge but NOT the structural-root edge.
   - Calling `Match(senderNodeType="", signalType="terminal/success")` returns the structural-root edge AND the cross-cutting edge (assuming the cross-cutting `when:` matches).
5. Run `go build ./lib/graph/node/... && go test ./lib/graph/node/... -count=1` and confirm.

### Task 4: Seed the implicit `""` entry in `templateDeclaredMessageTypes`

**Files:** `lib/runtime/subscription_loaders.go`

**Steps:**

1. Read `declaredMessageTypesForTemplate` (lines 110-132).
2. After the existing loop that populates the set from `row.Spec.Messages`, add: `set[""] = struct{}{}`. This seeds the implicit `""` entry into every template's declared-types set.
3. Update the function's GoDoc to note the implicit `""` seeding, citing `@decision: empty-message-as-root-trigger`.
4. Run `go build ./lib/runtime/... && go test ./lib/runtime/... -run TestDeclared -count=1` and confirm.

### Task 5: Tighten the empty-type rejection to "reserved-for-runtime"

**Files:** `lib/graph/node/template_validator.go`, `lib/graph/node/template_validator_test.go`

**Steps:**

1. Read `validateMessages` in `template_validator.go` lines 3115-3135. Note the existing check at lines 3122-3131 that already rejects an empty `Type` with the message `"type is required"`. This task does not add a new check — it tightens the existing one.
2. Replace the `"type is required"` error message with a more specific reserved-for-runtime diagnostic. The block becomes:
   ```go
   for i, m := range spec.Messages {
       base := fmt.Sprintf("messages[%d]", i)
       t := strings.TrimSpace(m.Type)
       if t == "" {
           res.Errors = append(res.Errors, ValidationError{
               Path: base + ".type",
               Msg:  `type "" is reserved-for-runtime (the implicit empty-message wake trigger seeded automatically at registration; author-declared empty-type entries are refused)`,
           })
           continue
       }
       // ... rest of the loop unchanged
   ```
3. Update or add a unit test in `template_validator_test.go` asserting:
   - A template declaring `messages: [{ type: "", body_schema: {} }]` is rejected at registration with the new reserved-for-runtime error message (verify by exact substring match on `"reserved-for-runtime"`).
   - A template with no `""` declaration registers cleanly (sanity check).
4. Run `go test ./lib/graph/node/... -run TestTemplateValidator -count=1` and confirm.

### Task 6: Drop the registration-side reserved-property check for `wake_node_ids`

**Files:** `lib/graph/node/template_validator.go`, `lib/graph/node/template_validator_test.go`

**Steps:**

1. In `template_validator.go` around line 3296, locate the check that rejects `body_schema:` declaring the reserved top-level property `wake_node_ids`. Delete it (the property is no longer reserved post-spec; this is part of the synthetic-envelope mechanism retirement).
2. In `template_validator_test.go`, locate and delete the test case(s) that asserted templates declaring `body_schema: { wake_node_ids: ... }` were rejected (grep for `wake_node_ids` in the file).
3. Run `go test ./lib/graph/node/... -count=1` and confirm.

### Task 7: Mutate `concept:message-schema`

**Files:** `.ok-planner/design/concepts/message-schema.md`

**Steps:**

1. Read the current file.
2. Remove the invariant beginning "`body_schema:` must not declare the reserved top-level property `wake_node_ids`; this is the runtime-synthetic wake mechanism's field name…" entirely.
3. Add a new invariant (at the appropriate place in the Invariants list):
   > Every template's declared-types set carries an implicit `""` entry seeded at registration with a null body schema. The implicit entry has no fields and no substitution references can resolve against it; receivers gate on the entry via subscription edges, not via body substitution. An author-declared `messages:` entry of type `""` is refused at registration as reserved-for-runtime.
4. Write the file with the changes.

### Task 8: Mutate `concept:node-subscription`

**Files:** `.ok-planner/design/concepts/node-subscription.md`

**Steps:**

1. Read the current file.
2. Replace the Owns section's first bullet (currently beginning "The per-template inverse-edge map data structure…") with:
   > The per-template inverse-edge map data structure (keyed by `(sender_node_type, type-path-prefix)`; a per-sender radix tree / prefix-bucket structure computed at template registration), populated from `subscribes:` entries plus runtime-injected structural-root edges. The runtime-injected edges are keyed by sender=`""`, one per structural root receiver (a node whose author-declared `subscribes:` block is empty or absent), with `wake_on_change: true`, `force_upstream_refresh: false`, type-pattern matching `terminal/success`, and `sender_bound_to_empty: true`. The augmentation is template-determinable and lives on the runtime's derived in-memory map; the canonical template hash is over the spec bytes only and is unaffected by the derived view.
3. Add to the Invariants list:
   > Every subscription edge carries a `sender_bound_to_empty` flag. Cross-cutting (`instance: true`) edges set it false (consulted on every settled sender, matched against the sender's signal). Runtime-injected structural-root edges set it true (consulted only when the actual settling sender's type is `""`). Author-declared `subscribes:` entries cannot set the flag; the runtime owns it.
4. Write the file with the changes.

### Task 9: Mutate `concept:cascade`

**Files:** `.ok-planner/design/concepts/cascade.md`

**Steps:**

1. Read the current file.
2. In the Boundaries section, replace the paragraph that describes the two edge maps (currently "The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map…") with:
   > The cascade walker consults two edge maps — the subscription-edge map and the upstream-refresh edge map. Both feed the wait-set with the same row shape. Subscription edges are keyed by sender node-type (downstream lookup from a transitioning sender); upstream-refresh edges are keyed by receiver node-type (upstream lookup from a freshly-invalidated receiver), so the walker can proactively invalidate upstreams that a receiver names in a `subscribes:` entry with `force_upstream_refresh: true`. Under the subscription-edge map's `""` sender-key, two kinds of edges coexist — cross-cutting subscriptions (fire on every settled sender, when the subscriber's predicate matches) and runtime-injected structural-root edges (fire only when the actual sender's type is `""`, i.e., the implicit empty-message virtual). The walker disambiguates via the edge's `sender_bound_to_empty` flag; both kinds use the same code path otherwise.
3. Write the file with the changes.

### Task 10: Create three new decisions + mutate one existing decision

**Files:**
- `.ok-planner/design/decisions/empty-message-as-root-trigger.md` (new)
- `.ok-planner/design/decisions/structural-root-edge-injection-at-registration.md` (new)
- `.ok-planner/design/decisions/empty-sender-key-edge-disambiguation.md` (new)
- `.ok-planner/design/decisions/subscription-edges-only-from-explicit-block.md` (existing)

**Steps:**

1. Create `decisions/empty-message-as-root-trigger.md` with frontmatter `decision: empty-message-as-root-trigger\nstatus: as-is`, title "# Empty message as the universal root trigger", and three sections (Choice / Rationale / Alternatives considered) — body text per the spec's Design changes entry for this decision.
2. Create `decisions/structural-root-edge-injection-at-registration.md` similarly, body per the spec.
3. Create `decisions/empty-sender-key-edge-disambiguation.md` similarly, body per the spec.
4. Read `decisions/subscription-edges-only-from-explicit-block.md`. Replace its Choice and Rationale sections per the spec's Design changes entry for this existing decision.
5. Run `go build ./... && go test ./lib/graph/node/... ./lib/runtime/... -count=1` and confirm. (This pass leaves the existing synthetic-envelope code intact; everything still compiles and existing tests pass.)

---

## Pass 2: Migrate operator-facing callers + harness CreateInstance

**Goal:** Remove the synthetic-envelope tail call from `handleCreateInstance` and `handleResetNode`; delete the asset-materialize endpoint entirely (handler + route + CLI + MCP + action); add the compose driver's empty-message emit; update the test harness's `CreateInstance` helpers to emit an empty-message wake after the create POST (so existing `waitForRootDispatch` still works). After this pass, only the test harness's `node/invalidate` caller remains on the synthetic-envelope path. Tree compiles and the testcontainers scenario suite still passes (the harness's wake-after-create preserves their entry assumption).

**Scope:** Tasks 11–23

**Falsifier:** `code:lib/control/controlapi/instances.go::handleCreateInstance` still calls `runtime.EnqueueSyntheticWakeFrame`, OR `code:lib/control/controlapi/nodes.go::handleResetNode` still calls `runtime.EnqueueSyntheticWakeFrame`, OR `route:POST /instances/{id}/assets/{alias}/materialize` is still registered, OR `code:lib/control/controlapi/mcp_route.go` still declares the `asset_materialize` tool schema, OR `code:lib/control/controlapi/actions.go` still includes the `asset:materialize` action row, OR the compose driver does not emit an empty message between `ApplyPlan` and the wait-for-terminal loop, OR `code:test/support/scenario/harness.go::CreateInstance` does not emit an empty message after the create POST and before `waitForRootDispatch`.

### Task 11: Drop the `instance/root` synthetic-envelope emission from `handleCreateInstance`

**Files:** `lib/control/controlapi/instances.go`

**Steps:**

1. Read lines 1395-1482 of `instances.go` (the post-create logic that builds `rootWakeIDs` and calls `EnqueueSyntheticWakeFrame`).
2. Delete the entire block from line 1395 (the comment "The block below seeds the first frame…") through line 1474 (closing the `EnqueueSyntheticWakeFrame` block). The `return createInstanceResponse{...}` at lines 1476-1481 stays.
3. Delete the `subgraphInternal` map and the `rootWakeIDs` computation (these were used only for `wake_node_ids`).
4. Remove any now-unused imports (run `goimports -w lib/control/controlapi/instances.go`).
5. Run `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -run TestCreateInstance -count=1`.

### Task 12: Drop the synthetic-envelope wake from `handleResetNode`

**Files:** `lib/control/controlapi/nodes.go`

**Steps:**

1. Read lines 195-289 of `nodes.go` (the reset handler).
2. The state gate at lines 195-200 (refusing non-failed state with `409 Conflict`) stays unchanged. The state-machine clear at lines 217-238 (UpdateError, ResetFailedTerminalSettlingSignalType, SetFrameID) stays unchanged.
3. Delete the entire block at lines 239-267 — the comment "drive the reset through the frame engine…" through the closing of the `EnqueueSyntheticWakeFrame` call's outer transaction. The audit-event Append at lines 268-286 stays unchanged.
4. Remove any now-unused imports.
5. Run `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -run TestResetNode -count=1`. Some existing tests may assert that the reset opens a frame; those failures are expected and will be fixed in the proof-update task for `story:node-admin`.

### Task 13: Delete `handleAssetMaterialize` and the asset-materialize route

**Files:** `lib/control/controlapi/assets.go`, `lib/control/controlapi/routes.go` (or wherever `Post("/instances/{id}/assets/{alias}/materialize", ...)` is registered)

**Steps:**

1. Read `assets.go` to locate `handleAssetMaterialize`, `materializeRequest`, `errAssetMaterializeUnknownNode`, and `findNodeByType`.
2. Delete `handleAssetMaterialize` (the entire function and its inner helpers/types specific to it).
3. Delete the `materializeRequest` type.
4. Delete `errAssetMaterializeUnknownNode` type and its `Error()` method.
5. Grep for other uses of `findNodeByType` in `lib/control/controlapi/` — if it's used only by `handleAssetMaterialize`, delete it too. If used elsewhere, leave it.
6. Find the route registration `Post("/instances/{id}/assets/{alias}/materialize", ...)` (likely in a routes file or in `assets.go` itself) and delete the line.
7. Delete any test helpers or fixtures in `assets_test.go` that exclusively support the materialize handler (grep for `handleAssetMaterialize` and `materialize` callers in test files).
8. Run `go build ./lib/control/controlapi/...` and confirm; remove unused imports.

### Task 14: Delete the `asset_materialize` MCP tool schema

**Files:** `lib/control/controlapi/mcp_route.go`

**Steps:**

1. At line 160 of `mcp_route.go`, delete the `"asset_materialize"` schema entry (one line including the schema JSON).
2. Run `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -run TestMCP -count=1`.

### Task 15: Delete the `asset:materialize` action row

**Files:** `lib/control/controlapi/actions.go`

**Steps:**

1. At lines 510-512 of `actions.go`, delete the `asset:materialize` action row (3 lines).
2. Grep `lib/control/controlapi/` for any other references to `"asset:materialize"` — if any role-template or grant fixtures mention it, remove those references too.
3. Run `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -run TestActions -count=1`.

### Task 16: Delete the `rimsky asset materialize` CLI subcommand

**Files:** `cmd/rimsky/cli/asset.go`, the CLI dispatcher (likely `cmd/rimsky/cli/main.go` or a `dispatcher.go`), `cmd/rimsky/cli/asset_test.go`

**Steps:**

1. In `asset.go`, delete `RunAssetMaterialize` (starts at line 117) and its helper if any.
2. Find the dispatcher that wires `asset materialize` to `RunAssetMaterialize` (grep for `RunAssetMaterialize` outside `asset.go`); delete that dispatch entry.
3. Delete `MaterializeAsset` from `cmd/rimsky/cli/client.go` (line 1024) and `MaterializeAssetRequest` if no other site uses it (grep first).
4. Delete the corresponding test in `asset_test.go` (grep for `Materialize` in that file).
5. Run `go build ./cmd/... && go test ./cmd/rimsky/cli/... -count=1`.

### Task 17: Add `Client.CreateInstanceMessage` to `cmd/rimsky/cli/client.go`

**Files:** `cmd/rimsky/cli/client.go`, `cmd/rimsky/cli/client_test.go`

**Steps:**

1. After `ListInstanceMessages` (around line 893) and `GetMessage` (around line 928), add a new method:
   ```go
   // CreateInstanceMessageRequest is the POST /instances/{id}/messages
   // body shape. Type is REQUIRED (the server-side validator at
   // `code:lib/control/controlapi/messages.go::postMessageRequest`
   // does not accept omitempty); the empty-string value is the valid
   // empty-message wake trigger.
   type CreateInstanceMessageRequest struct {
       Type    string          `json:"type"`
       Payload json.RawMessage `json:"payload,omitempty"`
   }

   // CreateInstanceMessageResponse is the POST /instances/{id}/messages
   // response body shape.
   type CreateInstanceMessageResponse struct {
       MessageID string `json:"message_id"`
   }

   // CreateInstanceMessage calls POST /instances/{id}/messages with the
   // supplied idempotency key in the `Idempotency-Key` header.
   func (c *Client) CreateInstanceMessage(ctx context.Context, instanceID string, idempotencyKey string, body CreateInstanceMessageRequest) (*CreateInstanceMessageResponse, error) {
       req, err := c.request(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(instanceID)+"/messages", body)
       if err != nil {
           return nil, err
       }
       if idempotencyKey != "" {
           req.Header.Set("Idempotency-Key", idempotencyKey)
       }
       var out CreateInstanceMessageResponse
       if err := c.do(req, &out); err != nil {
           return nil, err
       }
       return &out, nil
   }
   ```
2. Add a unit test in `cmd/rimsky/cli/client_test.go` (the existing client-test file; no `messages_test.go` exists in this directory) that exercises the round-trip. The server stub asserts the request hits `POST /v1/instances/abc/messages` with `Idempotency-Key: test-key`; the request body decodes to a struct with `Type == ""` (the field IS present in the JSON because the struct field has no `omitempty`); responds with `{"message_id":"m1"}`. The client returns a response carrying `MessageID == "m1"`.
3. Run `go test ./cmd/rimsky/cli/... -run TestClient -count=1`.

### Task 18: Compose driver emits empty message after each instance create

**Files:** `cmd/rimsky/cli/compose/run.go`

**Steps:**

1. Read `cmd/rimsky/cli/compose/run.go` lines 305-330 (the section after `ApplyPlan` returns and before the wait-for-terminal goroutine).
2. After the `created, err := ApplyPlan(...)` call succeeds and before `instanceIDs, keyByID := extractInstanceIDs(created)`, add a wake-emit loop:
   ```go
   // @constraint: instance creation is idle post-spec
   // (story:instance-create-is-idle). Emit an empty message to each
   // newly created instance via the universal message-emit path so the
   // structural roots wake and the wait-for-terminal loop has work to
   // observe. The Idempotency-Key is deterministic on the instance key
   // so a manifest re-run does not enqueue a second wake frame.
   // @decision: compose-driver-emits-empty-message-after-create
   // @story: one-shot-to-terminal
   for _, ci := range created {
       if ci.ID == "" {
           continue
       }
       wakeKey := "compose-wake-" + ci.Key
       if _, err := c.CreateInstanceMessage(bootCtx, ci.ID, wakeKey,
           cli.CreateInstanceMessageRequest{}); err != nil {
           fmt.Fprintln(os.Stderr, "rimsky compose run: emit wake message:", err)
           return coord.Drain(context.Background(), ReasonAnyFailure)
       }
   }
   ```
3. Run `go build ./cmd/... && go test ./cmd/rimsky/cli/compose/... -count=1`. Some compose-driver tests may need adjustment in the later proof-update pass for the new wake-emit assertion; basic compilation should pass here.

### Task 19: Mutate `concept:instance` Purpose section

**Files:** `.ok-planner/design/concepts/instance.md`

**Steps:**

1. Read the current file.
2. Replace the Purpose section with:
   > Templates declare the graph shape; instances are the live runtimes. Instances are what frames belong to and what cascade resolves against. Instance creation creates the per-instance row and the per-instance node rows and triggers the lifecycle-subscriber `OnInstanceCreated` callback; no frame is enqueued and no work begins until a sender posts a message. The empty-message trigger (`story:empty-message-wakes-roots`) is the universal convenience for waking every structural root without crafting a typed envelope.
3. Write the file.

### Task 20: Mutate `concept:asset` Owns and Invariants

**Files:** `.ok-planner/design/concepts/asset.md`

**Steps:**

1. Read the current file.
2. Replace the Boundaries Owns clause with:
   > Owns: the compound definition, the control-api asset endpoints (list, detail, versions, materialization-history, delete), the matching CLI asset subcommands, the dashboard asset-primary panel. Does NOT own: any new primitive (assets are claims; see `concept:claim`, `concept:claim-lifetime`); re-materialization triggering (operators express re-materialization via messages — empty for whole-instance, typed for template-author-designed partial paths).
3. Remove the invariant "The asset-materialize endpoint drives the asset's producer to (re)materialize the asset by opening a new frame whose triggering message wakes the producer node."
4. Write the file.

### Task 21: Mutate the three affected stories (asset-management, instance-lifecycle, node-admin)

**Files:**
- `.ok-planner/design/stories/asset-management.md`
- `.ok-planner/design/stories/instance-lifecycle.md`
- `.ok-planner/design/stories/node-admin.md`

**Steps:**

1. Mutate `stories/asset-management.md` per the spec's Design changes entry — Role, Capability, Business value, Acceptance, Falsifier all replaced.
2. Mutate `stories/instance-lifecycle.md` per the spec — Acceptance replaced; Role / Capability / Business value / Falsifier / Proof unchanged.
3. Mutate `stories/node-admin.md` per the spec — Role, Capability, Business value, Acceptance, Falsifier all replaced.
4. Write all three files.

### Task 22: Create three new decisions for this pass

**Files:**
- `.ok-planner/design/decisions/node-reset-as-pure-retry-budget-clear.md` (new)
- `.ok-planner/design/decisions/asset-materialize-endpoint-retired.md` (new)
- `.ok-planner/design/decisions/compose-driver-emits-empty-message-after-create.md` (new)

**Steps:**

1. Create `decisions/node-reset-as-pure-retry-budget-clear.md` with frontmatter and three sections (Choice / Rationale / Alternatives considered) per the spec's Design changes entry.
2. Create `decisions/asset-materialize-endpoint-retired.md` similarly per the spec.
3. Create `decisions/compose-driver-emits-empty-message-after-create.md` similarly per the spec.

### Task 23: Add `Harness.PostInstanceMessage` helper + wire it into CreateInstance for the wake step

**Files:** `test/support/scenario/harness.go`

**Steps:**

1. Read `test/support/scenario/harness.go` around lines 505-560 (the `CreateInstance` and `CreateInstanceWithOverrides` helpers) and around lines 561+ to see how `CreateInstanceWithServiceBindings` handles auth-key headers — match the prevailing pattern for `Authorization` / `Idempotency-Key` headers and JSON body construction on `POST /v1/instances/...`.
2. Add a new helper method `Harness.PostInstanceMessage` that wraps `POST /v1/instances/{id}/messages`. This helper is used by Task 26 and Task 28 in addition to Task 23 itself, so it lands as one helper used three places (single idiom for in-test message-emit). Place it near `CreateInstance` in the file. Signature:
   ```go
   // PostInstanceMessage posts a typed (or empty-typed) message to the
   // instance via POST /v1/instances/{id}/messages. msgType may be ""
   // for the empty-message wake trigger. payload may be nil. The
   // idempotencyKey MUST be unique per call site to avoid replay-200.
   //
   // @decision: test-harness-create-instance-wakes-roots-after-create
   // @decision: test-harness-invalidate-node-retired
   func (h *Harness) PostInstanceMessage(instanceID shared.UUID, msgType string, payload []byte, idempotencyKey string) shared.UUID {
       h.T.Helper()
       bodyMap := map[string]any{"type": msgType}
       if len(payload) > 0 {
           bodyMap["payload"] = json.RawMessage(payload)
       }
       body, err := json.Marshal(bodyMap)
       if err != nil {
           h.T.Fatal(err)
       }
       url := h.ControlBase + "/v1/instances/" + instanceID.String() + "/messages"
       req, err := http.NewRequest(http.MethodPost, url, bytesReader(body))
       if err != nil {
           h.T.Fatalf("PostInstanceMessage: build: %v", err)
       }
       req.Header.Set("Content-Type", "application/json")
       req.Header.Set("Idempotency-Key", idempotencyKey)
       // Mirror any Authorization-header pattern from CreateInstanceWithServiceBindings if one is required for /messages.
       resp, err := http.DefaultClient.Do(req)
       if err != nil {
           h.T.Fatalf("PostInstanceMessage: post: %v", err)
       }
       defer resp.Body.Close()
       if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
           buf := make([]byte, 4096)
           n, _ := resp.Body.Read(buf)
           h.T.Fatalf("PostInstanceMessage: status %d: %s", resp.StatusCode, string(buf[:n]))
       }
       var out struct {
           MessageID string `json:"message_id"`
       }
       if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
           h.T.Fatalf("PostInstanceMessage: decode: %v", err)
       }
       id, err := parseUUIDStr(out.MessageID)
       if err != nil {
           h.T.Fatalf("PostInstanceMessage: bad message_id %q: %v", out.MessageID, err)
       }
       return id
   }
   ```
3. In `CreateInstanceWithOverrides` (which `CreateInstance` delegates to), between the `parseUUIDStr(out.InstanceID)` call (line 553-554) and the `h.waitForRootDispatch(id, 5*time.Second)` call (line 557), insert a call to the new helper:
   ```go
   // @constraint: instance creation is idle post-spec
   // (story:instance-create-is-idle). The harness emits an empty
   // wake message so the existing waitForRootDispatch semantics still
   // hold without per-test changes.
   // @decision: test-harness-create-instance-wakes-roots-after-create
   h.PostInstanceMessage(id, "", nil, "harness-wake-"+id.String())
   ```
4. The existing `h.waitForRootDispatch(id, 5*time.Second)` call (line 557) stays as the next step.
5. Run `go test ./test/scenarios/ -run TestInstanceLifecycle -count=1` and confirm a test that exercises `CreateInstance` still receives a dispatched instance.

---

## Pass 3: Harness InvalidateNode retires + dependent test reinstrument + synthetic envelope retirement + cleanup

**Goal:** Delete `Harness.InvalidateNode` entirely (both branches). Retire the 2 surface-tests whose subject was the operator-invalidate surface. Reinstrument the 13 scaffolding tests with legitimate triggers — 12 via typed-message (Task 26), 1 via real executor + callback for parked-state (Task 27). Reinstrument the 4 story-proof call sites (3 stories) with typed-message-driven invalidation (Task 28). With no remaining callers of `EnqueueSyntheticWakeFrame`, delete `code:lib/runtime/synthetic_envelope.go` and clean up every site that referenced it (frame engine, receipt-side reserved-field check, registration-side reserved-property check, substitution.go sanctioned-read-site enumeration, dependent control-api tests, retired blessed-invariant annotations). Tree compiles, all scenario tests pass at the END of the pass.

**Important — mid-pass tree state is intentionally broken.** Task 24 deletes `Harness.InvalidateNode`, which immediately breaks compilation across 19 call sites in 17 test files. Tasks 25-28 fix all 19 sites (2 retire, 12 typed-message reinstrument, 1 executor+callback reinstrument, 4 story-proof reinstrument). Compilation is restored at the end of Task 28. Tasks 29-36 then do the rest of the cleanup. **Do not run `go build ./...` between Task 24 and Task 28** — compile errors during this range are expected, caused by the deletion in Task 24, and resolved by the sweep in Tasks 25-28. The pass-boundary contract is "compiles at end of Pass 3," not "compiles at end of every task within Pass 3."

**Scope:** Tasks 24–36

**Falsifier:** `Harness.InvalidateNode` still exists in `code:test/support/scenario/harness.go`, OR `TestOperatorInvalidateTargetOnly` still exists in `code:test/scenarios/lifecycle_handlers_test.go`, OR `TestParkedLifecycleResumeOnExternalInvalidate` still exists in `code:test/scenarios/parked_lifecycle_test.go`, OR any of the 17 reinstrument call sites (12 in Task 26 + 1 in Task 27 + 4 in Task 28) still calls a now-deleted `h.InvalidateNode` (would fail to compile), OR `code:lib/runtime/synthetic_envelope.go` still exists, OR `code:lib/graph/frame/engine.go::advanceOneFrame` still reads `wake_node_ids` or `wait_set_pairs` from message payloads, OR `code:lib/control/controlapi/messages.go` still defines or uses `errPayloadCarriesReservedField`, OR `code:lib/graph/attribute/substitution.go` lines 485-499 / 547-559 still enumerate `advanceOneFrame`'s `wake_node_ids` reader as a sanctioned read site, OR any test file still asserts on `instance/root` or `wake_node_ids` shapes.

### Task 24: Delete `Harness.InvalidateNode`

**Files:** `test/support/scenario/harness.go`

**Steps:**

1. Read `harness.go` around lines 874-929 (the helper's GoDoc starts at 874; the `InvalidateNode` function declaration is at line 889; both branches follow).
2. Delete the entire function — the GoDoc comment block at lines 874-888 AND the function body at lines 889-929.
3. Run `go build ./test/support/...` — this will fail because the dependent tests still call `InvalidateNode`. The next tasks fix those call sites. (Note: do NOT run the full test suite yet.)

### Task 25: Retire the 2 surface-tests

**Files:**
- `test/scenarios/lifecycle_handlers_test.go`
- `test/scenarios/parked_lifecycle_test.go`

**Steps:**

1. In `lifecycle_handlers_test.go`, delete the entire test function `TestOperatorInvalidateTargetOnly` (around line 430-460; locate by `func TestOperatorInvalidateTargetOnly`). If the test imports become unused after deletion, run `goimports -w` to clean them up. If there is any test-fixture template that was used only by this test, delete it too.
2. In `parked_lifecycle_test.go`, delete the entire test function `TestParkedLifecycleResumeOnExternalInvalidate` (around line 160-200; locate by `func TestParkedLifecycleResumeOnExternalInvalidate`). Same cleanup pattern.
3. Run `go build ./test/scenarios/... 2>&1 | grep -E 'lifecycle_handlers|parked_lifecycle'` and confirm those two files no longer contribute to the compile-error set.

### Task 26: Reinstrument 12 scaffolding tests with the legitimate-trigger pattern (per-file)

**Files:** (12 files, 12 call sites — `subscription_cascade_test.go` has two call sites but they live in two distinct test functions and are treated as two sub-tasks below)
- `test/scenarios/instance_lifecycle_fullstack_test.go` (line 139, target `w`)
- `test/scenarios/subscription_cascade_test.go` (line 152, target `a` in `TestSubscriptionCascade_EligibilityRespectsMultipleSenders`)
- `test/scenarios/subscription_cascade_test.go` (line 324, target `worker` in `TestSubscriptionCascade_CrossCuttingNegative`)
- `test/scenarios/observability_latest_attribute_fullstack_test.go` (line 81, target `w`)
- `test/scenarios/all_upstream_gating_test.go` (line 153, target `a`)
- `test/scenarios/pure_cascade_test.go` (line 54, target `hub`)
- `test/scenarios/multi_hard_dep_test.go` (line 162, target `trigN`)
- `test/scenarios/cascade_invalidate_test.go` (line 106, target `a`)
- `test/scenarios/no_op_commit_test.go` (line 121, target `producer`)
- `test/scenarios/per_run_attributes/substitution_test.go` (line 91, target `upN`)
- `test/scenarios/per_run_attributes/sequential_runs_test.go` (line 75, target `w`)
- `test/scenarios/breakpoints/soft_instance_pause_test.go` (line 75, target `n`)

**The uniform reinstrument pattern:**

Every reinstrument uses **per-target typed-message wake**. The target node gains a `subscribes:` entry to a test-only typed message, and the test posts that typed message at the call site where `h.InvalidateNode` was called. This works regardless of whether the target was previously a structural root or already downstream — the pattern is identical, with one extra step for previously-root targets to preserve the test's initial-dispatch behavior.

Why no "wake-all-roots via second empty message" shortcut for root targets: it perturbs sibling roots. Tests like `TestSubscriptionCascade_CrossCuttingNegative` (which asserts the sibling-root `monitor` MUST NOT fire when `worker` is invalidated) and `TestMultiHardDepRendezvous` (which pins single-frame cascade timing with `trigger`, `a`, `b` all as roots) explicitly depend on sibling-root quiescence during invalidation. Empty-message wake fires every root; that breaks their assertions. The uniform typed-message pattern preserves sibling-root quiescence in every case.

**The pattern, per call site:**

1. **Add a `messages:` entry to the template fixture.** Use a per-target slug like `test/wake/<target-type>` so each call site's invalidation is independently targetable. Shape (matching neighbor tests that use `messages:` — read e.g. `lib/services/test/scenarios/` for examples):
   ```go
   // In the template's Messages: ... block
   {Type: "test/wake/<target-type>", BodySchema: nil}
   ```
2. **Add a `subscribes:` entry to the target node.** The target subscribes to the typed message above:
   ```go
   // In the target node's WithSubscribes(...) call
   node.SubscriptionEntry{Node: "test/wake/<target-type>", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}
   ```
3. **For previously-root targets only — add a setup-phase typed-message emit.** Adding the `subscribes:` entry in step 2 demotes the target from structural-root status (the root-detection logic at `code:lib/control/controlapi/instances.go::handleCreateInstance` lines 1434-1448 checks `s.Node != "" && s.Node != def.Type`, and Task 2's `BuildSubscriptionEdges` augmentation replicates that logic). So the harness's `CreateInstance` empty-wake (Task 23) no longer fires the target initially. To preserve the test's initial-dispatch behavior, add a typed-message emit immediately after `h.CreateInstance` returns, before the test's initial assertions:
   ```go
   iid := h.CreateInstance(tid, "ck-...", map[string]any{})
   // @constraint: target was previously a structural root; the
   // subscribes: entry added for the typed-message wake demoted it
   // from root, so the harness's empty-wake doesn't fire it. Emit
   // the typed message here to drive the initial dispatch the test
   // assertions expect.
   h.PostInstanceMessage(iid, "test/wake/<target-type>", nil, fmt.Sprintf("test-wake-%s-init", t.Name()))
   ```
   For targets that already had `subscribes:` entries (downstream targets), skip this step — they fire via cascade from the empty-wake.
4. **Replace the `h.InvalidateNode(iid, X.ID)` call** with a typed-message emit using a UNIQUE per-call-site Idempotency-Key (so a re-run of the test produces a fresh frame, not a 200-OK replay):
   ```go
   h.PostInstanceMessage(iid, "test/wake/<target-type>", nil, fmt.Sprintf("test-wake-%s-<call-site-id>", t.Name()))
   ```
   Use a stable `<call-site-id>` integer (e.g., `1`, `2`) for tests with multiple invalidations of the same target. For previously-root targets where step 3 added a setup emit with suffix `-init`, the invalidation emit's suffix differs (e.g., `-1`).
5. **Update any waiting logic** in the test that previously waited for the target to dispatch after `InvalidateNode` — confirm the wait still works (the typed-message wake produces a new frame and dispatches the subscriber the same way the old synthetic envelope did).
6. Run `go test ./test/scenarios/ -run <TestName> -count=1` per file and confirm the test passes with the new mechanism.

**Steps (apply per file):**

For each of the 12 files above:

1. **Classify the target node.** Read 30 lines around the `h.InvalidateNode` call site to see the template fixture. Determine whether the target node (named in the call) currently has any `subscribes:` entries. If none → previously-root; step 3 of the pattern applies. If any → already-downstream; step 3 of the pattern is skipped.
2. **Apply the uniform pattern's steps 1, 2, 4, 5** to every file. **Apply step 3** only when the target was previously a root.
3. Run the per-file test and confirm.

**The `Harness.PostInstanceMessage` helper used here is added in Task 23 (single idiom, used by Tasks 23, 26, 28).**

Expected per-file change: ~10-15 lines for downstream targets (messages + subscribes + call replacement); ~15-20 lines for previously-root targets (same + setup-phase emit). The 12 files split roughly evenly between the two cases per spot-check.

### Task 27: Reinstrument `TestParkedHoldsFrame_EndToEnd` with real executor + callback

**Files:** `test/scenarios/parked_holds_frame_e2e_test.go`

**Steps:**

1. Read the existing test (around lines 90-130, where `h.InvalidateNode` is called at line 116).
2. Understand the test's intent: it exercises parked-state holding a frame open. The `InvalidateNode` call was being used to wake the worker back into a dispatch path so the test can observe the parked → terminal transition.
3. Replace the `h.InvalidateNode` call with a real callback emission: post to the supervisor's callback URL for the parked node-run. The harness already has helpers for the callback pattern — grep for `AsyncCallbackBody` or `/v1/callback/` in `test/support/scenario/` for the existing pattern (used by other parked-state tests). The callback body shape is documented at `proto:executor.proto::AsyncCallbackBody`.
4. The test fixture's executor stub may already be emitting the park outcome correctly. Confirm by reading the executor configuration in the test setup.
5. Run `go test ./test/scenarios/ -run TestParkedHoldsFrame_EndToEnd -count=1` and confirm.

### Task 28: Reinstrument 4 story-proof call sites (3 stories) with typed-message-driven invalidation

**Files:**
- `test/scenarios/claim_handoff_durable_e2e_test.go` (story: claim-handoff-durable, 1 call site at line 210)
- `test/scenarios/explicit_attribute_context_read_test.go` (story: explicit-attribute-context-read + upstream-pull-on-invalidate, 1 call site at line 327)
- `test/scenarios/per_run_attributes/hard_dep_test.go` (story: upstream-pull-on-invalidate, 2 call sites at lines 149 and 299)

**Steps:**

For each file:

1. Apply Task 26's uniform per-target typed-message wake pattern. For each call site: classify the target as previously-root or already-downstream; add `messages:` + `subscribes:`; for previously-root targets, add the setup-phase typed-message emit after `h.CreateInstance`; replace the `h.InvalidateNode` call with the typed-message emit.
2. For `hard_dep_test.go`, the two call sites (lines 149 and 299) each target a different node (`aN.ID` and `cN.ID` respectively). Classify each independently. Use a per-target typed-message slug (e.g., `test/wake/a` and `test/wake/c`) so the two call sites' invalidations are independently targetable.
3. The four affected stories' `Acceptance` and `Proof` fields are unchanged per `## Proof changes` (all A. Preserve); the proof artifacts get updated mechanism, not new outcomes.
4. The `@blessed-invariant: upstream-staled-before-receiver-dispatch` annotation in `hard_dep_test.go` (line ~197) retires as part of the synthetic-envelope mechanism retirement — delete the annotation. The cascade walker's existing upstream-refresh-edge-map handling covers `force_upstream_refresh: true` for typed messages (see Mechanism narrative in the spec), so the story's observable outcome holds without the slug.
5. The `Harness.PostInstanceMessage` helper used here is the one added in Task 23 (single idiom, used by Tasks 23, 26, 28). Task 27 uses a different idiom (callback URL POST) appropriate to its parked-state subject.
6. Run `go test ./test/scenarios/... -run 'TestClaimHandoff_Durable|TestStoryReadWithoutWaking|TestPerRunAttributes_HardDepPullsUpstream' -count=1` and confirm all four pass.

### Task 29: Delete `lib/runtime/synthetic_envelope.go`

**Files:** `lib/runtime/synthetic_envelope.go`

**Steps:**

1. Grep `lib/` `cmd/` `test/` for any remaining callers of `EnqueueSyntheticWakeFrame` or `expandWakeWithUpstreamRefresh`: `grep -rn 'EnqueueSyntheticWakeFrame\|expandWakeWithUpstreamRefresh' lib/ cmd/ test/`. **This task runs after Tasks 24-28 in Pass 3** — those tasks remove the only remaining callers (Task 24 deletes the helper; Tasks 25-28 reinstrument or retire the dependent tests). Expected grep result at this point: zero matches.
2. If zero matches, delete the file `lib/runtime/synthetic_envelope.go`.
3. Run `go build ./...` and confirm. Any compilation errors indicate a missed caller — fix or fail loudly.

### Task 30: Strip `wake_node_ids` / `wait_set_pairs` handling from `advanceOneFrame`

**Files:** `lib/graph/frame/engine.go`

**Steps:**

1. Read `lib/graph/frame/engine.go::advanceOneFrame` lines 240-400 (the function body, focusing on the wake-bearing payload extraction and the wait-set pre-install branch around lines 280-382).
2. Delete the block that reads `payloadMap["wake_node_ids"]` (around line 283) through any stale-marking it drives.
3. Delete the `wait_set_pairs` pre-install branch (around lines 338-382) — the entire `@blessed-invariant: upstream-staled-before-receiver-dispatch` block.
4. Remove the `@blessed-invariant: upstream-staled-before-receiver-dispatch` annotation from the function.
5. Remove the structural-divide comments (lines 180-237 area) that explained why the wake-mechanism divide is load-bearing — those described a divide that no longer exists post-spec.
6. Remove now-unused imports (`goimports -w lib/graph/frame/engine.go`).
7. Run `go build ./lib/graph/frame/... && go test ./lib/graph/frame/... -count=1`.

### Task 31: Remove the receipt-side reserved-field check `errPayloadCarriesReservedField`

**Files:** `lib/control/controlapi/messages.go`, `lib/control/controlapi/messages_test.go`

**Steps:**

1. Read `lib/control/controlapi/messages.go` lines 180-250 (the reserved-field guard block) AND lines 530-545 (the error-handling branch in `handleCreateMessage` that uses the sentinel).
2. Delete the following from `lib/control/controlapi/messages.go`:
   - `errPayloadCarriesReservedField` (sentinel error variable, around line 196).
   - `reservedPayloadFieldWakeNodeIDs` (constant, around line 191).
   - The comment block at lines 180-200 documenting the reserved-field guard.
   - The validation logic that checks for the reserved field (around lines 230-238 in `handleCreateMessage`).
   - The error-handling branch at lines 535-545 in `handleCreateMessage`:
     ```go
     if errors.Is(err, errPayloadCarriesReservedField) {
         writeJSON(w, http.StatusBadRequest, map[string]any{
             "error":          "reserved payload field",
             "reserved_field": reservedPayloadFieldWakeNodeIDs,
         })
         return
     }
     ```
3. Delete the reserved-field rejection test in `lib/control/controlapi/messages_test.go` around lines 622-651 (the test body that constructs a payload with `wake_node_ids` and asserts HTTP 400 with `resp.body["reserved_field"] == "wake_node_ids"`). Verify deletion is complete by grepping the file: `grep -n 'wake_node_ids\|reserved_field\|errPayloadCarriesReservedField' lib/control/controlapi/messages_test.go` → zero matches.
4. Verify deletion is complete by grepping the source: `grep -n 'errPayloadCarriesReservedField\|reservedPayloadFieldWakeNodeIDs' lib/control/controlapi/messages.go` → zero matches.
5. Run `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -count=1`.

### Task 32: Strip `wake_node_ids` from the sanctioned-payload-read-site enumerations

**Files:** `lib/graph/attribute/substitution.go`

**Steps:**

1. Read `lib/graph/attribute/substitution.go` lines 485-499 and 547-559 (the two `@blessed-invariant 21` sanctioned-read-site enumerations).
2. In each enumeration, remove the bullet/paragraph that names `advanceOneFrame`'s `wake_node_ids` reader. The remaining four sanctioned read sites stay intact.
3. Update the `@blessed-invariant 21` discriminator block at line 121 if it references the wake-node-ids reader (it likely cites the same enumeration).
4. Run `go build ./lib/graph/attribute/... && go test ./lib/graph/attribute/... -count=1`.

### Task 33: Update dependent control-api tests to drop instance/root and wake_node_ids assertions

**Files:** `lib/control/controlapi/app_test.go`, `lib/control/controlapi/messages_test.go`, `lib/control/controlapi/idempotency_matrix_test.go`, `lib/services/test/scenarios/control_api_idempotency_required_e2e_test.go`

**Steps:**

1. In `app_test.go` lines 438-447: locate the assertions about the post-create `instance/root` synthetic message and the auto-enqueued frame. Delete or rewrite them to assert the new idle behavior (no message in ledger, no frame).
2. In `messages_test.go` line 439: locate the assertion about `instance/root` behavior and delete/rewrite as above.
3. In `idempotency_matrix_test.go` line 57: locate the matrix entry referencing the instance/root path and remove or rewrite.
4. In `lib/services/test/scenarios/control_api_idempotency_required_e2e_test.go` lines 104 and 182: rewrite the assertions that touch instance/root behavior.
5. Run `go test ./lib/control/controlapi/... ./lib/services/test/scenarios/... -count=1 -short`. (The `-short` flag, if supported in this repo, skips testcontainers tests; if not, run the testcontainers tests with Docker available.)

### Task 34: Mutate `concept:wait-set` (drop insertion path b)

**Files:** `.ok-planner/design/concepts/wait-set.md`

**Steps:**

1. Read the current file.
2. Replace the Owns section's two-insertion-paths bullet with:
   > The single insertion path: on cascade-walk when a sender transitions out of a settled state (pessimistic invalidate). The settled-state drain bulk-marks rows as drained when the sender resolves (fresh / failed / parked) by stamping a drain timestamp rather than deleting the row.
3. Remove the invariant "Pre-emptive rows installed by synthetic-frame promotion (path b above) enter the same drain-on-settle lifecycle and feed the same eligibility predicate as cascade-walk rows; the receiver is gated until its declared force-upstream-refresh upstream settles and drains, which structurally guarantees the upstream runs before the receiver dispatches in the new frame."
4. Write the file.

### Task 35: Mutate `concept:message` (sender-kind paragraph clean-up)

**Files:** `.ok-planner/design/concepts/message.md`

**Steps:**

1. Read the current file.
2. Replace the "Two external emit sites and one internal" invariant paragraph with:
   > Two external emit sites and one internal: operator API (`POST /instances/{id}/messages` with `sender_kind: operator`), publisher emissions (the same endpoint with `sender_kind: publisher` + a publisher-subscription capability token), and cascade-emit (a message-emitter-node's dispatch, with `sender_kind: instance` and sender `instance:<id>`). All three paths land in the same ledger and follow the same delivery rules. `sender_kind: instance` is unambiguously cascade-emit; the runtime synthesizes no envelopes.
3. Replace the type-lookup invariant with:
   > Type lookup at receipt: a message whose `type` is not declared in the target template's message-schema registry is refused with an unknown-type response; loud miss, not silent dead-letter. Every template's declared-types set carries an implicit `""` entry seeded at registration, so empty-typed messages pass receipt under the same uniform check.
4. Write the file.

### Task 36: Create two new decisions and clean up retired blessed-invariant annotations

**Files:**
- `.ok-planner/design/decisions/synthetic-envelope-mechanism-retired.md` (new)
- `.ok-planner/design/decisions/test-harness-invalidate-node-retired.md` (new)
- `.ok-planner/design/decisions/test-harness-create-instance-wakes-roots-after-create.md` (new)

**Steps:**

1. Create `decisions/synthetic-envelope-mechanism-retired.md` with frontmatter and three sections (Choice / Rationale / Alternatives considered) per the spec's Design changes entry for this decision.
2. Create `decisions/test-harness-invalidate-node-retired.md` with frontmatter and three sections per the spec's Design changes entry.
3. Create `decisions/test-harness-create-instance-wakes-roots-after-create.md` with frontmatter and three sections per the spec's Design changes entry.
4. Grep for `@blessed-invariant: upstream-staled-before-receiver-dispatch` across the repo: `grep -rn '@blessed-invariant: upstream-staled-before-receiver-dispatch' .`. Expected: zero matches (the annotations at `code:lib/runtime/synthetic_envelope.go`, `code:lib/graph/frame/engine.go`, and `code:test/scenarios/per_run_attributes/hard_dep_test.go` were removed by the file/block deletions in earlier tasks). If any remain, delete them.
5. Run `go build ./... && go test ./lib/... ./test/scenarios/... -count=1` to confirm the entire repo compiles and the existing scenario suite passes.

---

## Pass 4: Acceptance pass — STORY-instance-create-is-idle, STORY-empty-message-wakes-roots

**Goal:** Create the two new story files and write the two new scenario tests that exhibit each story end-to-end via the rimsky-all-in-one stack. Both proofs run real testcontainers infrastructure and assert the user-observable outcomes the spec names.

**Scope:** Tasks 37–39

**Falsifier (STORY-instance-create-is-idle):** the create call returns success but a frame row exists for the instance with no operator-posted triggering message; OR a synthetic envelope appears in the message ledger immediately after create; OR a node-run row exists with no operator emission having occurred.

**Falsifier (STORY-empty-message-wakes-roots):** the empty-message emit lands in the ledger but no frame opens; OR the frame opens but no structural root stale-marks (no node-runs created); OR a non-root node with author-declared direct subscriptions (a `subscribes:` entry naming a specific upstream node-type, not the cross-cutting `instance: true` form) also stale-marks (the trigger overreaches); OR `Idempotency-Key` replay opens a second frame. Cross-cutting (`instance: true`) subscribers may legitimately fire when their `when:` predicate matches the empty-message virtual's `terminal/success` emission; their firing is not a falsifier.

### Task 37: Create story files

**Files:**
- `.ok-planner/design/stories/instance-create-is-idle.md` (new)
- `.ok-planner/design/stories/empty-message-wakes-roots.md` (new)

**Steps:**

1. Create `stories/instance-create-is-idle.md` with frontmatter `story: instance-create-is-idle\nstatus: as-is`, title "# Operator creates an idle instance", and Role / Capability / Business value / Acceptance / Falsifier / Proof sections matching the spec verbatim.
2. Create `stories/empty-message-wakes-roots.md` similarly, with sections matching spec STORY-empty-message-wakes-roots verbatim.
3. Write both files.

### Task 38: Create acceptance proof — STORY-instance-create-is-idle scenario test

**Files:** `test/scenarios/instance_create_is_idle/instance_create_is_idle_e2e_test.go` (new directory and file)

**Story:** STORY-instance-create-is-idle
**Proof form (from spec):** executable proof — `POST /instances` followed by `GET /instances/{id}/frames` and `GET /instances/{id}/messages` returning empty collections; one lifecycle-subscriber callback recorded.

**Steps:**

1. Create the directory `test/scenarios/instance_create_is_idle/` and write a Go test file with a top-of-file comment carrying `@story: instance-create-is-idle`.
2. The test boots the rimsky-all-in-one stack via testcontainers (use the helpers at `lib/services/test/scenarios/` for the harness pattern). **Important:** this test does NOT use `Harness.CreateInstance` (which wakes-after-create per TD-test-harness-create-instance-wakes-roots-after-create); it calls the control-api `Client` directly so it can observe the idle state. Pattern:
   - Deploy a simple template (one structural-root node + one downstream node).
   - `POST /instances` via `Client.CreateInstance` — assert response carries `instance_id`, `paused: false`, no terminal timestamp.
   - Sleep a small bounded interval (e.g., 200ms — enough that any synthetic envelope would have shown up; the assertion is about IDLE).
   - `GET /instances/{id}/frames` → assert the `frames` array is empty.
   - `GET /instances/{id}/messages` → assert the `messages` array is empty.
   - `GET /instances/{id}/nodes` → assert no node-runs exist (state `fresh` on every row, with `frame_id` null).
   - Register a lifecycle-subscriber test stub before the create. The closest test cousin is `test/scenarios/canary/lifecycle_subscriber_callback_test.go` — read it for the stub-registration pattern. Assert the stub received exactly one `OnInstanceCreated` callback.
3. Run the test: `go test ./test/scenarios/instance_create_is_idle/... -count=1`. The test requires Docker.

### Task 39: Create acceptance proof — STORY-empty-message-wakes-roots scenario test

**Files:** `test/scenarios/empty_message_wake/empty_message_wakes_roots_e2e_test.go` (new directory and file)

**Story:** STORY-empty-message-wakes-roots
**Proof form (from spec):** executable proof — emit empty message; observe one new frame with `triggering_message_id` matching the emit; observe stale-mark and dispatch on each structural root; observe non-root direct subscribers untouched (and cross-cutting subscribers fire iff their predicate matches); replay with the same key observes the original message id and no second frame.

**Steps:**

1. Create the directory `test/scenarios/empty_message_wake/` and write a Go test file with `@story: empty-message-wakes-roots`.
2. Test setup (testcontainers, all-in-one stack):
   - Deploy a template with: a structural root `root1` (no `subscribes:`), a structural root `root2` (no `subscribes:`), a downstream `down` (`subscribes: [{ node: root1, type: terminal/success, wake_on_change: true, force_upstream_refresh: false }]`), a cross-cutting watcher `watch` (`subscribes: [{ instance: true, type: terminal/success }]`).
   - `POST /instances` via Client. Assert idle (per STORY-instance-create-is-idle).
   - `POST /instances/{id}/messages` with `Header: Idempotency-Key: test-wake-1`, body `{"type":""}`. Assert response: `message_id`, `201 Created`.
3. Test observations after a bounded wait:
   - `GET /instances/{id}/frames` → assert exactly one frame, `triggering_message_id` matches returned `message_id`.
   - `GET /instances/{id}/nodes` → assert `root1` and `root2` have node-runs in the new frame; `down` does NOT have a node-run from this empty-message wake (will dispatch later as `root1` settles); `watch` MAY have a node-run (cross-cutting firing on terminal/success is legitimate).
   - Wait further for the frame to settle. Assert `root1` and `root2` dispatched; `down` then dispatched downstream of `root1`; `watch` dispatched.
4. Replay test:
   - `POST /instances/{id}/messages` with the same `Idempotency-Key: test-wake-1`, same body. Assert response carries original `message_id` with `200 OK`.
   - `GET /instances/{id}/frames` → assert still exactly one frame.
5. Run: `go test ./test/scenarios/empty_message_wake/... -count=1`.

---

## Pass 5: Update proof artifacts for mutated stories

**Goal:** Update the scenario tests that exhibit the four affected stories (`story:asset-management`, `story:instance-lifecycle`, `story:node-admin`, `story:one-shot-to-terminal`) so they reflect the new post-spec behavior. The first three are B (Shift the intent) — the proof artifacts get rewritten to exhibit the new outcomes. The last is A (Preserve the intent) — the compose-driver scenario test gains an assertion that the empty-message emit happens internally. (The 3 additional A. Preserve stories — `claim-handoff-durable`, `explicit-attribute-context-read`, `upstream-pull-on-invalidate` — were already handled in Pass 3 Task 28 as part of the harness retirement.)

**Scope:** Tasks 40–44

**Falsifier:** The asset-management scenario test still asserts the materialize-trigger sub-outcome OR fails to add an explicit-re-materialization-via-message variant; OR the instance-lifecycle scenario test does not assert post-create idle behavior OR does not exercise the empty-message wake step as a separate operator action; OR the node-admin scenario test still asserts force-invalidate OR does not exercise the two-step retry workflow (reset → empty-message); OR the compose-driver scenario test does not assert that an internal empty-message emit happens between `ApplyPlan` and the wait-for-terminal loop.

### Task 40: Create the asset-management proof artifact

**Files:** `test/scenarios/asset_management/asset_management_e2e_test.go` (new directory and file)

**Steps:**

1. Confirm by grep that no existing file currently carries `@story: asset-management`: `grep -rn '@story: asset-management' test/ lib/services/test/`. Expected: zero matches. (The existing files under `test/scenarios/asset/` exhibit other stories — durable lifetime, staging — not the operator-facing asset-management observation surface.)
2. Create the directory `test/scenarios/asset_management/` and write a Go test file with a top-of-file comment carrying `@story: asset-management`.
3. The test boots the rimsky-all-in-one stack via testcontainers and:
   - Deploys a template whose nodes declare durable claims against a data-processing-capable producer (the asset construction).
   - Creates an instance, posts an empty message (the wake step), waits for the producer to dispatch and materialize an asset.
   - Asserts `GET /instances/{id}/assets` lists the asset alias with its current version.
   - Asserts `GET /instances/{id}/assets/{alias}` returns detail.
   - Asserts `GET /instances/{id}/assets/{alias}/versions` returns the version history.
   - Asserts `GET /instances/{id}/assets/{alias}/materialization-history` returns rows that match the real dispatches observed.
   - Asserts `DELETE /instances/{id}/assets/{alias}` removes the alias.
4. Adds a separate sub-test for the explicit-re-materialization-via-message variant: post a second empty message; assert the producer dispatches again and a new materialization-history row appears as a result of that real dispatch (the trigger is now message-driven, not endpoint-driven).
5. Run the test and confirm.

### Task 41: Update instance-lifecycle proof artifact

**Files:** `test/scenarios/instance_lifecycle_fullstack_test.go`

**Steps:**

1. Read the existing file. Note that it currently has **no** `@story: instance-lifecycle` annotation; the linkage is implicit by name only.
2. Add a top-of-file GoDoc comment carrying the annotation (insert before the `package` declaration, alongside any existing copyright header):
   ```go
   // instance_lifecycle_fullstack_test.go — exhibits the operator-
   // driven instance-lifecycle story end-to-end: create, observe
   // post-create idle, post the empty-message wake, observe
   // dispatch, pause, resume, force-terminate, delete.
   //
   // @story: instance-lifecycle
   ```
3. After the `POST /instances` call (or its in-test equivalent), add an assertion that the instance's frame collection and message ledger are both empty before any wake step — this exhibits the post-spec idle-on-create behavior. **Note:** this test was modified in Pass 3 Task 26 (it was one of the 12 scaffolding reinstrument targets); the typed-message reinstrument from that task and the `@story` annotation from this task may need to be merged carefully. If the test was simplified in Task 26 to use `h.PostInstanceMessage` for a specific wake, the broader assertion of "frames/messages empty before wake" may need a fresh idle-instance create that bypasses `Harness.CreateInstance` (which wakes-after-create).
4. The pause / resume / force-terminate / delete sub-assertions stay unchanged.
5. Run `go test ./test/scenarios/ -run TestInstanceLifecycle -count=1` and confirm.

### Task 42: Update node-admin proof artifact

**Files:** `test/scenarios/node_admin_e2e_test.go`

**Steps:**

1. Read the existing file. Note that it currently has **no** `@story: node-admin` annotation; the linkage is implicit by name only.
2. Add a top-of-file GoDoc comment carrying the annotation:
   ```go
   // node_admin_e2e_test.go — exhibits the post-spec node-admin
   // operator surface end-to-end: inspect node state; clear retry
   // budget on failed-terminal nodes (with the 409-on-non-failed
   // gate preserved); the two-step retry workflow (reset followed
   // by an empty-message wake).
   //
   // @story: node-admin
   ```
3. Drop sub-tests that assert the force-invalidate-via-reset behavior and the in-cascade-option sub-capability.
4. Keep the existing `409 Conflict` assertion for non-failed-state resets (it's preserved per the spec).
5. Add a two-step retry workflow sub-test:
   - Drive a node to `failed-terminal` with an exhausted retry budget.
   - `POST /nodes/{id}/reset` → assert `200 OK` and the retry budget is cleared.
   - `POST /messages` with an empty body and an idempotency key → assert the cascade walker stale-marks the previously-failed node, a fresh acquisition attempt happens, and the node dispatches.
6. Run `go test ./test/scenarios/ -run NodeAdmin -count=1` and confirm (the actual test name is `TestAcceptance_NodeAdmin_GetAndReset`; `-run` substring-matches `NodeAdmin`).

### Task 43: Update compose-driver proof artifact (preserve intent)

**Files:** Locate the existing compose-driver scenario test (grep for `@story: one-shot-to-terminal` and `compose` under `lib/services/test/scenarios/` and `cmd/rimsky/cli/compose/`).

**Steps:**

1. Locate the file (likely `lib/services/test/scenarios/compose_*_e2e_test.go` or `cmd/rimsky/cli/compose/run_test.go`).
2. The story's Acceptance is unchanged ("every declared instance reaches terminal state"). The proof artifact still drives the compose manifest to terminal.
3. Add an internal-observation assertion: after `ApplyPlan` returns and before the wait-for-terminal loop, the compose driver emits one empty message per declared instance via `Client.CreateInstanceMessage`. Assert this happens by either:
   - Inspecting the instance's message ledger after the run: each instance's ledger should carry an empty-typed message with an Idempotency-Key matching the `compose-wake-<instance_key>` pattern.
   - Or intercepting at the cli.Client layer with a test-injected client whose `CreateInstanceMessage` records the calls (cleaner; matches the test pattern other compose tests use).
4. Confirm the `@story: one-shot-to-terminal` annotation is still present (the file at `code:cmd/rimsky/cli/compose/run.go` already carries it per the existing annotations at file top).
5. Run the test and confirm.

### Task 44: Final repo-wide verification

**Files:** none (verification only)

**Steps:**

1. Run `go build ./... && make lint && go test ./... -count=1`. All packages compile, lint clean, every test passes.
2. Grep for any remaining references to the retired surfaces, confirming zero matches:
   - `grep -rn 'EnqueueSyntheticWakeFrame' lib/ cmd/ test/`
   - `grep -rn 'wake_node_ids\|wait_set_pairs' lib/ cmd/ test/`
   - `grep -rn 'asset/materialize\|node/invalidate\|instance/root\|node/reset' lib/ cmd/ test/` (excluding documentation strings about the retirement itself — the runtime no longer emits or consumes these type-paths)
   - `grep -rn 'errPayloadCarriesReservedField\|reservedPayloadFieldWakeNodeIDs' lib/`
   - `grep -rn '@blessed-invariant: upstream-staled-before-receiver-dispatch' lib/ test/`
   - `grep -rn 'InvalidateNode\b' lib/ cmd/ test/` — expected: zero matches in production code; zero matches in test code (the helper retired entirely, all callers retired or reinstrumented).
3. If any greps return unexpected matches, surface them and fix.

---

## Manual checks after completion

None. Every verification in this plan is expressible as a `go test` or `grep` command; nothing requires manual UI or human-judgment review. The two new acceptance scenario tests (Tasks 38-39) exhibit the new stories end-to-end via testcontainers; the proof artifacts for mutated stories (Tasks 40-43) regenerate from the new behavior. The final verification (Task 44) confirms zero residue of the retired surfaces and zero residue of `InvalidateNode`.
