# Cascade and claim-handoff implementation plan

**Spec:** `.ok-planner/specs/2026-06-10-cascade-and-claim-handoff-design.md`
**Goal:** Land the four claim-handoff and cascade user-outcome stories with executable proofs, fix the dispatch-time substitution-context gap that drops `acq.HeldClaims`, and sharpen the `serial_queue` wording in `concept:frame`.
**Architecture:** Five passes — one acceptance pass per story (each delivering its scenario-test proof and creating its durable story file), plus a final design-doc sharpen of `design/concepts/frame.md`. The first acceptance pass also carries the only known code fix (the `buildResolveContextForDispatch` substitution merge); the other three stories' proofs presume existing code already delivers the outcome — if any proof fails, the implementer fixes the underlying code in the same pass per the necessity rule.
**Tech Stack:** Go (root module `github.com/rimsky-ai/rimsky-core`), `go-chi/chi`, `jackc/pgx/v5`, `modernc.org/sqlite`, testcontainers-go for scenario tests. Scenario tests live under `test/scenarios/...` and use the existing `test/support/scenario` harness.

---

## Load-bearing properties

These are the properties the spec depends on. Each implementing task names the property and the cheaper shape that must not be used.

- **Wire-payload parity (STORY-claim-handoff):** the bytes a co-holder receives via substitution for `{{claim.<alias>.address}}` are byte-equal to the bytes the acquirer received. Do NOT plumb a "best guess" or re-look-up; merge the same `claimproducer.ClaimResult` value from `acq.HeldClaims` (populated at the co-holder's own acquire-tx) into the substitution context.
- **Acquired-claims-win on alias collision (STORY-claim-handoff):** when a co-holder both `claims:` an alias AND `holds:` that same alias, the `acq.Locks` entry wins; the `acq.HeldClaims` entry is informational only. This mirrors the existing precedence at `lib/runtime/runner_dispatch.go::buildStoreHandles` (an `acq.Locks`-bound alias is not overwritten by `acq.HeldClaims`).
- **Auto-terminal atomicity (STORY-claim-handoff, -across-frames):** Commit fires exactly when every node in the holding subgraph is non-active. The proof must drive every member to settled state and assert the `claim_handle` row reaches `state = committed` (or `state = abandoned` on the failure variant) only after the slowest member settles, not earlier.
- **Cross-frame held-claim survival (STORY-claim-handoff-across-frames):** the `claim_handle` row stays `state = active` across frame boundaries until the entire holding subgraph completes. Do NOT verify by reading slog `frame.start`/`frame.end` log lines (those are not persisted events); verify by reading the `claim_handle` row's `state` column and the dispatched run rows' `frame_id` columns.
- **Cross-dispatch durable persistence (STORY-claim-handoff-durable):** a `lifetime: durable` `claim_handle` row survives past the producing dispatch's terminal AND past a forced retention-sweep tick. Do NOT skip the retention-sweep force — sweep skipping is the durable-exemption invariant, and a test that doesn't force a sweep proves nothing.
- **Cascade signal-blindness (STORY-cascade-signal-blind):** the same code path delivers every cascade-firing signal type to its subscribers. The proof's per-row assertions must drive a REAL settlement that emits the canonical envelope (not a hand-crafted signal injection); use `error_types` policies for the `terminal/error/<class>` rows so the envelope flows through the real `lib/runtime/on_error.go` → `emitSignalInTxOnce` path.

---

## Pass 1: Substitution-context held-claims merge — acceptance pass — STORY-claim-handoff

**Goal:** Fix the dispatch-time substitution context to include `acq.HeldClaims` and land the scenario-test proof + durable story file for STORY-claim-handoff.

**Scope:** Tasks 1–3.

**Falsifier:** `lib/runtime/runner_dispatch.go::buildResolveContextForDispatch` still builds `claims` only from `acq.Locks` and omits `acq.HeldClaims`, OR the scenario test exists but doesn't exercise the regression-close shape (co-holder reading `{{claim.<alias>.address}}` → Commit), OR doesn't exercise the per-field substitution kinds (`.address`, `.payload.<f>`, `.claim_scope`), OR doesn't exercise the Abandon path, OR doesn't exercise multi-co-holder, OR doesn't exercise wire-payload parity (co-holder's `StoreHandle` bytes equal acquirer's), OR `design/stories/claim-handoff.md` is absent or doesn't carry the spec's STORY-claim-handoff body verbatim.

### Task 1: Merge `acq.HeldClaims` into the dispatch-time substitution context

**Files:** `lib/runtime/runner_dispatch.go`

**Load-bearing properties enforced:** wire-payload parity; acquired-claims-win on alias collision.

The current implementation at `lib/runtime/runner_dispatch.go::buildResolveContextForDispatch` (around line 664) builds the `claims` map from `acq.Locks` only:

```go
claims := map[string]claimproducer.ClaimResult{}
for _, lk := range acq.Locks {
    if lk.Alias == "" {
        continue
    }
    claims[lk.Alias] = lk.ClaimResult
}
```

`acq.HeldClaims` is populated at the co-holder's own acquire-tx (see `lib/runtime/runner_acquire.go` around line 691 calling `loadInheritedClaimsForNode`) but never merged into the substitution context. Three adjacent paths already drain `acq.HeldClaims` correctly: `lib/runtime/runner_locks.go::buildLockSpecs` (lock/store-selector substitution at acquire-time), `lib/runtime/runner_acquire_helpers.go::substituteFanOutPartitionRequest` (fan-out partition substitution), and `lib/runtime/runner_dispatch.go::buildStoreHandles` (executor wire payload). The dispatch-time attribute-schema substitution context is the only one that drops them — exactly the gap reported in GH issue #16.

**Steps:**

1. Open `lib/runtime/runner_dispatch.go`. Locate `buildResolveContextForDispatch` (it begins around line 664 with `func buildResolveContextForDispatch(ctx context.Context, args RunArgs, acq *acquisition, partitionKey string) (attributes.ResolveContext, error)`). Confirm the `claims` map is populated from `acq.Locks` only and that `acq.HeldClaims` is not referenced in the function.
2. After the existing `for _, lk := range acq.Locks { ... }` loop and before the `var paramsRaw json.RawMessage` declaration, insert a second loop merging `acq.HeldClaims` into `claims`:

   ```go
   // Held claims (per the node's template `holds:` block) are populated at
   // the co-holder's own acquire-tx by loadInheritedClaimsForNode. They
   // carry the same `claimproducer.ClaimResult` shape (Address + ClaimScope)
   // that an opened claim carries, so the substitution grammar resolves
   // `{{claim.<alias>.address|payload.<f>|claim_scope}}` identically whether
   // the alias was opened (acq.Locks) or co-held (acq.HeldClaims).
   //
   // Acquired claims win on alias collision: if the node both `claims:`
   // and `holds:` the same alias, the opened entry in claims[] is
   // authoritative and the held entry is informational. Mirrors the
   // precedence at buildStoreHandles below.
   //
   //	@concept: claim-co-holdership
   for alias, held := range acq.HeldClaims {
       if _, alreadyPresent := claims[alias]; alreadyPresent {
           continue
       }
       claims[alias] = held
   }
   ```

3. Run `go build ./lib/runtime/...` and confirm it compiles.
4. Run `go vet ./lib/runtime/...` and confirm no warnings.

### Task 2: Add scenario-test proof for STORY-claim-handoff

**Files:** `test/scenarios/claim_handoff_e2e_test.go` (new)

**Story:** STORY-claim-handoff
**Proof form (from spec):** Executable proof — table-driven scenario test covering five shapes: regression-close, per-field substitution kinds, Abandon path, multi-co-holder Commit, wire-payload parity.

**Load-bearing properties enforced:** wire-payload parity (byte-equal assertion); auto-terminal atomicity (only fires after slowest holder settles).

**Steps:**

1. Create `test/scenarios/claim_handoff_e2e_test.go` with package `scenarios`. Follow the existing pattern from `test/scenarios/held_claim_acquirer_passes_test.go` for harness setup (stub claim producer via `test/support/stores/stub/store`, harness via `test/support/scenario`).
2. Implement a `TestClaimHandoff_E2E` test that table-drives across five subcases. For each subcase, deploy a fresh template and create an instance; invalidate the acquirer; wait for terminal states; assert.

   **Subcase A — regression-close.** Two-node template: acquirer with `Stores: [{ Name: <stub>, Alias: "schema", Intent: rw }]`; co-holder with `Subscribes: [{ Node: "acquirer", Type: "terminal/success" }]`, `Holds: { "schema": { From: "acquirer" } }`, and an attribute schema reading `"{{claim.schema.address}}"` into a string-typed attribute. Drive acquirer + co-holder to fresh. Assert: the co-holder's `MergedAttributes` (read via the runner-emitted lineage row or the per-run attribute table) contains the substituted address bytes equal to the acquirer's `claim_handle` row's `Address` column; the `claim_handle` row reaches `state = committed`, `is_held = true`.

   **Subcase B — per-field substitution.** Same template shape, but with three attribute fields reading `{{claim.schema.address}}`, `{{claim.schema.payload.<f>}}` (use a stub claim-producer payload key like `payload.region`), and `{{claim.schema.claim_scope}}`. Assert each resolves to the corresponding column on the acquirer's `claim_handle` row (`Address`, `Payload`-extracted field, `ClaimScopeData`). The stub claim producer's `Open` response should carry a payload with the named field — confirm via `stubstore.Config` how to seed the payload.

   **Subcase C — Abandon path.** Same template, but the co-holder is forced to settle `terminal/error/<class>` via `ErrorTypes: { "test/forced": { Policy: [{ Action: "give_up" }] } }` and the stub returns an error of that class. Drive both to terminal. Assert: the `claim_handle` row reaches `state = abandoned`.

   **Subcase D — multi-co-holder Commit.** Three-node template: one acquirer + two co-holders both declaring `Holds:` against the acquirer's alias and both reading `{{claim.schema.address}}`. Drive all three to fresh. Assert: BEFORE the second co-holder settles, the `claim_handle.state` is still `active` (read it during a deliberate stagger using the harness's wait-for-state); only after both co-holders settle does it transition to `committed`. This is the atomicity property — auto-terminal does not fire on the first co-holder's settlement alone.

   **Subcase E — wire-payload parity.** Same as Subcase A, plus assert byte-equality between the acquirer's `claim_handle.Address` column and the bytes the substitution engine resolves into the co-holder's substituted attribute for `{{claim.<alias>.address}}`. Read both from persistence inside one `h.Persist.Transaction(...)` block: discover the acquirer's claim handle UUID via `h.Persist.ClaimHandles().ListByHolderNode(ctx, acquirerNodeID, tx)` (method at `lib/foundation/persistence/claim_handles.go:190`); read the row via `h.Persist.ClaimHandles().Get(ctx, claimHandleID, tx)` (the table method is `Get`, not `GetByID` — see `lib/foundation/persistence/claim_handles.go:189`); read the co-holder's substituted attribute value via `h.Persist.NodeAttributes().GetByRun(ctx, coHolderRunID, tx)` returning a `*NodeAttributesRow` whose `.Data` is the resolved attributes map. Compare the address bytes (JSON-encoded if necessary) with `bytes.Equal`. This asserts the parity at the contract surface the spec describes — the bytes the co-holder consumes equal the bytes the acquirer received — without requiring changes to `test/support/executors/stub/stub.go::ObservedRequest`.

3. Run `go build ./test/scenarios/...` and confirm it compiles.
4. Run `go test -count=1 ./test/scenarios -run TestClaimHandoff_E2E` (testcontainers requires Docker). Confirm all five subcases pass. If any subcase fails because the underlying code path doesn't yet deliver the outcome, the implementer fixes the underlying code in this same pass per the spec's necessity-rule framing (the spec carries no TD for additional code work, but a story whose proof exposes a real bug compels the fix as part of delivering the story).

### Task 3: Create `design/stories/claim-handoff.md`

**Files:** `.ok-planner/design/stories/claim-handoff.md` (new)

**Steps:**

1. Create `.ok-planner/design/stories/claim-handoff.md` with the following content. Frontmatter is exactly two fields (`story:` + `status:`); body sections are Role / Capability / Business value / Acceptance / Falsifier / Proof, matching the existing story-file shape (cf. `design/stories/template-subscriptions.md`). Do NOT add a `## Notes` section — the current-state-only rule (see `.ok-planner/CLAUDE.md`) forbids dated audit-trail entries on durable artifacts.

   ```markdown
   ---
   story: claim-handoff
   status: as-is
   ---

   # Template author wires multi-node atomic staging via claim handoff

   ## Role

   As a template author building a multi-node atomic-staging workflow, I can declare an upstream acquirer node that opens a claim and downstream co-holder nodes that share the same claim via `holds:` — reading the live claim's address, payload fields, and scope bytes via `{{claim.<alias>.address|payload.<f>|claim_scope}}` to do work against the staged location — then have the runtime fire Commit (all-success) or Abandon (any-failed) atomically across the holding subgraph, so that I compose stage-then-write-then-verify-then-commit pipelines (and similar all-or-nothing patterns) without re-acquiring the same claim from every node.

   ## Capability

   A downstream node declaring `holds: { <alias>: { from: <upstream-type> } }` co-holds the upstream's claim by alias; at dispatch the runtime resolves `{{claim.<alias>.address}}`, `{{claim.<alias>.payload.<key>}}`, and `{{claim.<alias>.claim_scope}}` substitutions against the held claim's actual bytes — the same acquired result the original acquirer received. Auto-terminal fires once every node in the holding subgraph settles non-active: Commit on all-success, Abandon on any-failed.

   ## Business value

   Multi-node atomic-staging composes naturally from existing template-DSL primitives. The author writes one acquirer plus N co-holders; rimsky enforces the all-or-nothing guarantee without bespoke rollback logic in template-land.

   ## Acceptance

   A template with (a) an acquirer node opening a claim with alias X via its `stores:` declaration, and (b) a co-holder declaring `holds: { X: { from: <acquirer-type> } }` AND reading `{{claim.X.address}}` (or `.payload.<f>` or `.claim_scope`) in its attribute schema. When the acquirer is invalidated and settles `terminal/success`, the co-holder dispatches with the substitution resolved to the held claim's actual bytes — the address bytes the co-holder receives equal the bytes the acquirer received. When the co-holder also settles `terminal/success`, the held claim's auto-terminal fires Commit (the holding subgraph promotes to committed; the producer's Commit verb fires). When either the acquirer or the co-holder settles failed, auto-terminal fires Abandon.

   ## Falsifier

   The co-holder's `{{claim.X.address}}`, `{{claim.X.payload.<f>}}`, or `{{claim.X.claim_scope}}` substitution fails at dispatch with `terminal/error/template_resolution_failed`, OR the co-holder dispatches but receives substituted bytes that don't equal the acquirer's bytes, OR auto-terminal fails to fire Commit when every holding-subgraph member settles fresh, OR fails to fire Abandon when any member settles failed.

   ## Proof

   Executable proof — table-driven scenario test covering the regression-close shape (acquirer + co-holder reading `{{claim.X.address}}` → Commit), per-field substitution kinds (`.address`, `.payload.<f>`, `.claim_scope` each resolve to the held claim's bytes), the Abandon path (co-holder forced to terminal-error via `error_types: give_up` → Abandon), the multi-co-holder Commit shape (two co-holders both reading; auto-terminal fires only after the slowest settles), and wire-payload parity (a co-holder receives a store-handle wire entry identical to what the acquirer receives — same handle bytes regardless of whether the receiver opened the claim or co-held it). Pins `concept:claim-co-holdership` invariant "At dispatch, the co-holder's execution request carries the co-held claim's address (the same acquired result the original acquirer received)."
   ```

2. Confirm the file's frontmatter parses (it should — the structure matches existing stories).

---

## Pass 2: Held claim survives the frame boundary — acceptance pass — STORY-claim-handoff-across-frames

**Goal:** Land the scenario-test proof + durable story file for STORY-claim-handoff-across-frames.

**Scope:** Tasks 4–5.

**Falsifier:** the scenario test doesn't exist OR doesn't exercise all three variants (`frame: next` per-node subscription, `instance: true` cross-cutting subscription, three-frame chain), OR doesn't assert that the `claim_handle` row stays `state = active` across the frame boundary, OR doesn't assert distinct frame ids on the acquirer's vs co-holder's runs, OR `design/stories/claim-handoff-across-frames.md` is absent or doesn't carry the spec's STORY body verbatim.

### Task 4: Add scenario-test proof for STORY-claim-handoff-across-frames

**Files:** `test/scenarios/claim_handoff_across_frames_e2e_test.go` (new)

**Story:** STORY-claim-handoff-across-frames
**Proof form (from spec):** Executable proof — three scenario variants: `frame: next` per-node subscription, `instance: true` cross-cutting subscription, three-frame chain.

**Load-bearing properties enforced:** cross-frame held-claim survival.

**Steps:**

1. Create `test/scenarios/claim_handoff_across_frames_e2e_test.go` with package `scenarios`. Same harness pattern as Task 2.
2. Implement three test functions (or one test with three subcases — pick whichever matches existing conventions in `test/scenarios/`). Each follows the STORY-claim-handoff-across-frames Acceptance shape:

   **Variant 1 — `frame: next` per-node subscription.** Acquirer + co-holder; co-holder's `Subscribes` entry sets `Frame: "next"`. Drive acquirer to fresh. Wait for co-holder to dispatch. Assert: (a) the acquirer's run row and the co-holder's run row carry distinct `frame_id` UUIDs; (b) at the moment between the acquirer's settlement and the co-holder's settlement, the `claim_handle` row's `state` column is still `active` (use the harness's wait-for-state with a brief stagger, or insert a hook that pauses the co-holder briefly); (c) only after the co-holder settles does the `claim_handle.state` transition to `committed`.

   **Variant 2 — `instance: true` cross-cutting subscription.** Same shape but with `Subscribes: [{ Instance: true, Type: "terminal/success" }]` on the co-holder. The runtime defaults this to `Frame: "next"` per `concept:node-subscription`. Assert the same three properties as Variant 1.

   **Variant 3 — three-frame chain.** Acquirer + two co-holders, each subscribing with `Frame: "next"`, each declaring `Holds:` against the upstream alias, each reading `{{claim.X.address}}`. Assert: three distinct `frame_id` values across the three runs; the `claim_handle.state` stays `active` until the third frame's co-holder settles; the substituted address bytes on each co-holder equal the acquirer's `Address`.

3. Run `go build ./test/scenarios/...` and confirm it compiles.
4. Run `go test -count=1 ./test/scenarios -run TestClaimHandoff_AcrossFrames` (or whichever names you chose). Confirm all variants pass. If any variant fails because the underlying code path doesn't deliver the outcome, the implementer fixes the underlying code in this same pass per the necessity rule.

### Task 5: Create `design/stories/claim-handoff-across-frames.md`

**Files:** `.ok-planner/design/stories/claim-handoff-across-frames.md` (new)

**Steps:**

1. Create the file with frontmatter `story: claim-handoff-across-frames`, `status: as-is`. Body sections Role / Capability / Business value / Acceptance / Falsifier / Proof, exactly copying the STORY-claim-handoff-across-frames body from `.ok-planner/specs/2026-06-10-cascade-and-claim-handoff-design.md`. No `## Notes` section.

   The body content is the spec's STORY-claim-handoff-across-frames sections (Role, Capability, Business value, Acceptance, Falsifier, Proof) — copy them verbatim into the file body, with one `# Title` line above them. Use the spec's STORY-claim-handoff-across-frames "Role." paragraph as the basis for the `# Title` (something like "# Template author wires a claim handoff that survives across frames").

2. Confirm the file's frontmatter parses.

---

## Pass 3: Durable held claim survives across instance dispatches — acceptance pass — STORY-claim-handoff-durable

**Goal:** Land the scenario-test proof + durable story file for STORY-claim-handoff-durable.

**Scope:** Tasks 6–7.

**Falsifier:** the scenario test doesn't exist OR doesn't exercise all five subcases (cross-dispatch persistence, cross-dispatch `holds:`, conflict-detection-includes-committed-durable, asset-Release path, instance-termination release), OR doesn't FORCE a retention sweep tick to prove the durable-exemption invariant, OR doesn't assert via real persistence-layer state (only via in-memory return values), OR `design/stories/claim-handoff-durable.md` is absent or doesn't carry the spec's STORY body verbatim.

### Task 6: Add scenario-test proof for STORY-claim-handoff-durable

**Files:** `test/scenarios/claim_handoff_durable_e2e_test.go` (new)

**Story:** STORY-claim-handoff-durable
**Proof form (from spec):** Executable proof — five subcases listed below.

**Load-bearing properties enforced:** cross-dispatch durable persistence (force a retention-sweep tick).

**Steps:**

1. Create `test/scenarios/claim_handoff_durable_e2e_test.go` with package `scenarios`. Use the high-level `test/support/scenario.Harness` (same harness as Pass 1 / Pass 2 / Pass 4) — this test drives the full runtime stack end-to-end. The `test/scenarios/asset/durable_lifetime_*_test.go` family uses a lower-level postgres-only harness and is reference reading for the released-row-shape contract only, NOT for harness boilerplate. The retention-sweep helper lives at `lib/runtime/sweep_claim_handle_retention.go::SweepClaimHandleRetention`; the call shape is shown at `lib/runtime/sweep_claim_handle_retention_test.go:110` and at `test/scenarios/asset/durable_lifetime_e2e_test.go:110` (the pg-harness flavor). Adapt for the scenario harness: invoke it directly with `runtime.SweepClaimHandleRetention(ctx, h.Persist.ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})` — `h.Persist` is the field on `test/support/scenario.Harness` (see `test/support/scenario/harness.go:56`), `ClaimHandleTable.Get` is the row accessor (see `lib/foundation/persistence/claim_handles.go:189`).
2. Implement five subcases:

   **Subcase A — cross-dispatch persistence.** Deploy a template whose acquirer declares a claim with `Lifetime: durable`. Drive the acquirer to fresh in dispatch D1. Discover the acquirer's `claim_handle` UUID via `h.Persist.ClaimHandles().ListByHolderNode(ctx, acquirerNodeID, tx)` (`lib/foundation/persistence/claim_handles.go:190`). Assert the row has `state = committed`, `is_held = true`. Force a retention-sweep tick by invoking `runtime.SweepClaimHandleRetention(ctx, h.Persist.ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})` directly. Re-read the row via `h.Persist.Transaction(...) → h.Persist.ClaimHandles().Get(ctx, claimHandleID, tx)`; assert it is still present and `state = committed`.

   **Subcase B — cross-dispatch `holds:`.** From the same instance, trigger a second dispatch D2 that has a node declaring `Holds:` against the original upstream's alias. Assert: D2's co-holder dispatches successfully (no `terminal/error/template_resolution_failed`); the substituted `{{claim.<alias>.address}}` bytes equal the persisted `claim_handle.Address` from D1. D2's co-holder settles fresh.

   **Subcase C — conflict detection includes committed-durable.** While the durable row is `committed-durable` (state = committed, lifetime = durable), deploy a SECOND template whose acquirer attempts to `Open` the same claim scope. Assert the second acquirer settles `terminal/error/acquire/unavailable`. (See `test/scenarios/acquire_unavailable_*_test.go` for harness patterns around forcing scope conflicts.)

   **Subcase D — asset Release path.** Hit the control-API DELETE `/v1/instances/{instance_id}/assets/{alias}` endpoint (see `lib/control/controlapi/assets.go` for the route shape). Confirm the row is removed from the active-scope set. Re-attempt the conflicting acquirer from Subcase C; assert it now succeeds.

   **Subcase E — instance-termination release.** Open another `Lifetime: durable` claim on a fresh instance, drive to committed-durable, then exercise the held-durable-release path through the operator delete flow. Two HTTP calls in sequence are required — termination is the precondition for delete, and only delete invokes `ReleaseHeldDurableClaims`:
   1. `POST /v1/instances/{idOrKey}/terminate` (handler `handleTerminateInstance` at `lib/control/controlapi/instances.go:203`). Sets `terminated_at` on the instance row; this satisfies DELETE's `terminated_at IS NOT NULL` precondition guard (see `instances.go:716`).
   2. `DELETE /v1/instances/{idOrKey}` (handler `handleDeleteInstance` at `lib/control/controlapi/instances.go` — this is the only caller of `runtime.ReleaseHeldDurableClaims` in the live tree; the call site is around line 817).

   The HTTP pattern is established by existing scenarios (cf. `test/scenarios/instance_lifecycle_fullstack_test.go:191` and `test/scenarios/lifecycle_force_terminate_fullstack_test.go:80`): use `http.Post(h.ControlBase + "/v1/instances/" + iid.String() + "/terminate", ...)` and a corresponding `http.NewRequest("DELETE", ...)` against the same base.

   After the DELETE returns 200, assert: the `claim_handle` row is gone (`h.Persist.ClaimHandles().Get(...)` returns nil); the instance row is also gone (`h.Persist.Instances().Get(...)` returns nil — DELETE removes the row entirely, see `instances.go:839`).

3. Run `go build ./test/scenarios/...` and confirm it compiles.
4. Run `go test -count=1 ./test/scenarios -run TestClaimHandoff_Durable`. Confirm all subcases pass. Implementer fixes any underlying gaps per the necessity rule.

### Task 7: Create `design/stories/claim-handoff-durable.md`

**Files:** `.ok-planner/design/stories/claim-handoff-durable.md` (new)

**Steps:**

1. Create the file with frontmatter `story: claim-handoff-durable`, `status: as-is`. Body sections Role / Capability / Business value / Acceptance / Falsifier / Proof copied verbatim from the spec's STORY-claim-handoff-durable. No `## Notes` section.
2. Confirm frontmatter parses.

---

## Pass 4: Cascade fires subscribers uniformly across signal types — acceptance pass — STORY-cascade-signal-blind

**Goal:** Land the scenario-test proof + durable story file for STORY-cascade-signal-blind.

**Scope:** Tasks 8–9.

**Falsifier:** the scenario test doesn't exist OR doesn't iterate over every cascade-firing signal type in the canonical taxonomy (`terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`, `event/<name>`), OR doesn't exercise BOTH per-sender (`{ node: X, type: ... }`) AND cross-cutting (`instance: true`) subscription shapes, OR doesn't include the per-sender `terminal/error/*` row (the GH-issue-#15 regression-close shape), OR doesn't assert the audit row lands in `rimsky_events`, OR `design/stories/cascade-signal-blind.md` is absent.

### Task 8: Add scenario-test proof for STORY-cascade-signal-blind

**Files:** `test/scenarios/cascade_signal_blind_e2e_test.go` (new)

**Story:** STORY-cascade-signal-blind
**Proof form (from spec):** Executable proof — table-driven scenario test iterating over the cascade-firing signal types and asserting per-sender and cross-cutting subscriber dispatch plus audit-row presence.

**Load-bearing properties enforced:** cascade signal-blindness — drive real settlements through the runtime, do not hand-inject signals.

**Steps:**

1. Create `test/scenarios/cascade_signal_blind_e2e_test.go` with package `scenarios`. Read `test/scenarios/subscription_cascade_test.go` for the closest cousin (it already exercises one row of this table: `instance: true` + `terminal/error/stub/rate_limited` at `TestSubscriptionCascade_CrossCuttingPositive`).
2. Implement a `TestCascadeSignalBlind_E2E` test table-driven across the cascade-firing signal types. For each type, two variants: per-sender subscription and cross-cutting (`instance: true`) subscription.

   The table rows and how to drive each emit:

   - `terminal/success` (per-sender + cross-cutting). Drive the stub executor to `Success(...)`. Subscriber subscribes to `terminal/success`.
   - `terminal/error/<class>` give_up flavor (per-sender + cross-cutting; **regression close for GH issue #15**). Drive the stub executor to error with a specific class. Sender's `ErrorTypes` maps that class to `give_up`. Subscriber subscribes to `terminal/error/*` (trailing-`*` for the per-sender variant) and to `instance: true` + `terminal/error/<class>` for the cross-cutting variant. Both subscribers MUST dispatch.
   - `terminal/error/<class>` pass flavor (per-sender + cross-cutting). Same shape, but the `ErrorTypes` action is `pass` (settles fresh, but still emits `terminal/error/<class>`). Both subscribers MUST dispatch.
   - `transient/retry/<n>/<class>` (per-sender). Drive the stub to error with `retry` policy that has a non-zero attempt limit. Subscriber subscribes to `transient/retry/*`. Assert the subscriber dispatches on the retry emit.
   - `attribute/<key>/changed` (per-sender). Drive the stub executor's `Success` with a changed attribute key. Subscriber subscribes to `attribute/<key>/changed`. Assert dispatch.
   - `event/<name>` (per-sender). Drive the stub executor's `Success` with a named event. Subscriber subscribes to `event/<name>`. Assert dispatch.

   For each row, in addition to asserting subscriber dispatch, query `rimsky_events` for an audit row matching the emitted signal type and instance id; assert the row is present.

3. Run `go build ./test/scenarios/...` and confirm it compiles.
4. Run `go test -count=1 ./test/scenarios -run TestCascadeSignalBlind_E2E`. Confirm every row passes. The `terminal/error/<class>` per-sender row is the regression close for GH issue #15; if it fails on the current code despite the post-v0.6.0 signal-emit refactor at commit `6088bb0`, the implementer fixes the underlying code per the necessity rule (the refactor was supposed to close this; if it didn't, that's the bug to track down).

### Task 9: Create `design/stories/cascade-signal-blind.md`

**Files:** `.ok-planner/design/stories/cascade-signal-blind.md` (new)

**Steps:**

1. Create the file with frontmatter `story: cascade-signal-blind`, `status: as-is`. Body sections Role / Capability / Business value / Acceptance / Falsifier / Proof copied verbatim from the spec's STORY-cascade-signal-blind. No `## Notes` section.
2. Confirm frontmatter parses.

---

## Pass 5: Sharpen `concept:frame` `serial_queue` wording

**Goal:** Replace the misreadable `serial_queue` bullet in `.ok-planner/design/concepts/frame.md` with the boundary-crossing-invalidate clarification.

**Scope:** Task 10.

**Falsifier:** `.ok-planner/design/concepts/frame.md`'s `serial_queue` bullet still reads "Each invalidate produces its own frame" without the boundary-crossing qualifier, OR the new wording introduces forward-looking or backward-looking phrasing (forbidden by the current-state-only rule).

### Task 10: Edit `design/concepts/frame.md` to sharpen the `serial_queue` bullet

**Files:** `.ok-planner/design/concepts/frame.md`

**Steps:**

1. Open `.ok-planner/design/concepts/frame.md`. Locate the `serial_queue` bullet at line 28. The current text reads exactly:

   ```
   - **`serial_queue`** preserves ordering. Each invalidate produces its own frame; frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B").
   ```

2. Replace it with:

   ```
   - **`serial_queue`** preserves ordering. Each boundary-crossing invalidate (operator-API send or publisher-origin message) produces its own frame; cascade walks stay within the current frame. Frames run one at a time per instance. Right answer when each invalidate carries distinct semantics that must be processed in order (e.g. "process item A, then process item B").
   ```

3. Confirm no other section of `frame.md` needs touching — line 12 ("A frame *begins* only when a node is invalidated — a direct operator/user invalidation, or message delivery") and the Message-delivery paragraph already say the correct rule; the sharpen is localized to the `serial_queue` bullet only.
4. The new text is current-state-only: no `## Notes` entry, no "previously read X" lines, no dated audit-trail.

---

## Full verification

After Pass 5 completes, run the project's standard post-change checks (per the repo's `CLAUDE.md` "After Code Changes" section):

1. `go build ./...` — confirm the whole tree compiles.
2. `go test ./... -count=1` — confirm all tests pass. (Docker required for scenario tests via testcontainers.)
3. `make lint` — confirm lint is clean.
4. `go test -count=3 -race ./lib/runtime/... ./lib/foundation/persistence/...` — race-sensitive paths under stress (the substitution-context change in Pass 1 touches runtime).

If any check fails, fix forward and re-run the affected scope.

---

## Manual checks after completion

None. All four stories' proofs are executable; the concept-doc sharpen is a localized edit verifiable by reading the new bullet.
