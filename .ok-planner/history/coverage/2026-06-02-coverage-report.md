# Acceptance Coverage Report — 2026-06-02

Transient diagnostic. Regenerated whole on each `/coverage` run; not a registry,
safe to gitignore. It reads rimsky's own claims (README + control-api routes +
CLI verbs + gRPC protocols + the concept catalog), subtracts what is *really*
covered by a test meeting the real-acceptance bar, and emits the residue:
ranked acceptance gaps and drift symptoms.

**The bar applied (stricter than VERIFICATION.md's "behavioral"):** a claim is
*really covered* only if a test (a) drives the real entry point end-to-end
(real HTTP route / gRPC wire / CLI / assembled product, not a constructed
handler call); (b) exercises the claim's value-delivering component *for real* —
where the value is a component doing real work (a real sensor observing real
state, a producer doing a real swap), a stub does not count; where the value is
rimsky's own orchestration, the real control plane with a stub executor does;
and (c) asserts the real observable outcome (a persisted row, a wire call, a
node-state transition), not a struct/proto shape and not "no error".

## Summary

- **Claims enumerated: 71** concept-level claims (the durable design catalog under
  `.ok-planner/design/concepts/`), cross-referenced against the README's five
  headline patterns, the control-api routes, the CLI verbs, and the ten gRPC
  protocols under `lib/protocols/proto/v1/`.
- **Really covered: 61** — primary claim proven by a test meeting the full bar.
- **Real gaps (uncovered): 10** — implemented but no *real* acceptance test proves
  it. Two are whole-concept (`sensor`, `publisher`); the rest are a load-bearing
  sub-property or operator surface inside an otherwise-covered concept. (Includes
  the filesystem atomic-swap end-to-end gate — see gap #10 — **reclassified from a
  drift symptom on 2026-06-02 after grounding the filesystem store's real swap.**)
- **Drift symptoms: 1** — `VERIFICATION.md` over-claims complete behavioral
  coverage. (`atomic-staging` was *reclassified to a coverage gap*: the filesystem
  reference impl exists, does a real `os.Rename` swap on Commit, and is wire-tested
  — the concept doc is accurate, so there is nothing to retract.)

**Health read.** rimsky is *not* the classic pre-acceptance project — it carries a
large real end-to-end corpus (testcontainers Postgres scenarios via
`scenario.Start`, a services integration harness that drives real images via
testcontainers, persistence conformance on both real Postgres and real SQLite).
The gaps cluster tightly in two places: the **external-trigger surface** (a real
sensor/publisher process observing real state and firing a real downstream node
is never driven end-to-end) and the **agent-operator surface** (the MCP
`tools/call` skin and the operator dashboard's auth gate). Both are README
headline claims, so they carry the highest credibility risk despite the small
count.

---

## Coverage gaps (ranked)

Ranked by loudest-claim × weakest-coverage. Each gap's proposed acceptance
scenario becomes the spec's acceptance scenario when taken into `brainstorm` /
`write-plan`; `write-plan` turns it into the mandatory acceptance pass.

### 1. A real sensor never observes real external state and fires a real downstream node

- **Capability** — "Watching external state and reacting": a sensor observes an
  external system (a cron clock, an HTTP endpoint that changes) and the cascade
  fires the subscribing downstream node. This is the README's *first* headline
  pattern and `concept:cascade` calls reactivity-to-external-change "the killer
  primitive."
- **Source claim** — README §2 ("Watching external state and reacting"), §4
  ("Reactivity to external change (sensors emitting messages) is the same
  machinery"); `concept:sensor`.
- **Value-delivering component** — a *real* bundled sensor process
  (`sensor-cron`, `sensor-http` under `lib/services/sensors/`) observing real
  external state. This is a **component doing real work**, not orchestration.
- **Proposed acceptance scenario** — Start the real `sensor-http` image wired
  into a real rimsky all-in-one stack (the existing `lib/services/test/harness`
  testcontainers harness). Deploy a template whose node subscribes to the
  sensor's message kind. **Change the watched HTTP endpoint's body** → a real
  `message` row persists with `sender_kind: publisher`, and the subscribing
  downstream node flips to `stale` then re-runs to `fresh` via the cascade.
  (Cron variant: let a cron window elapse, assert the same.)
- **Why current coverage is nominal, not real** — every sensor proving test is
  client-and-server-in-one: `code:lib/services/sensors/sensor-cron/sensor_test.go::TestTick_FiresDueSubscriptionAndAdvances`
  and `code:lib/services/sensors/sensor-http/sensor_test.go::TestTick_PollsAndPushesOnChange`
  call `Tick()` in-process against a pinned clock and POST to a `httptest`
  *fake rimsky*; the smoke test `code:lib/services/test/smoke/data_platform_smoke_test.go::TestDataPlatformSmoke_SensorHTTP`
  never starts the sensor binary (its own comment admits it "mirrors the wire
  contract"). `lib/services/test/scenarios/` has **no sensor scenario** despite
  the harness already being able to boot real images.
- **Rank rationale** — loudest possible claim (headline pattern #1, "killer
  primitive") × zero real end-to-end coverage of the sensor's actual job.

### 2. The MCP skin's `tools/call` parity with the HTTP verbs is unproven

- **Capability** — every operator action on the control-api is also an MCP tool
  at `route:POST /mcp`, "so an LLM-driven supervisor can drive the platform on
  the same verbs a human would."
- **Source claim** — README §1 and §4 (the agent-operator surface); `concept:control-api`,
  `concept:permission`.
- **Value-delivering component** — the real MCP catalog→handler invoke path
  (orchestration: the MCP skin must reach the *same* handlers as the HTTP route).
- **Proposed acceptance scenario** — Against the real control-api (real chi
  router + real Postgres), perform `route:POST /instances` over HTTP **and** the
  equivalent `tools/call instance_create` over `route:POST /mcp`; assert both
  create an instance with the same response envelope, and that the MCP path
  writes an audit row tagged with the MCP skin.
- **Why current coverage is nominal, not real** — `tools/list` filtering and the
  `mcp:read` gate are really covered (`code:test/scenarios/auth/lifecycle_test.go::TestMCPSkin_RequiresMCPReadGate`).
  But every `tools/call` test uses a `fakeCatalog` whose `Invoke` records the
  name and returns `{"name": name}` (`code:lib/control/controlapi/mcp/server_test.go`),
  or drives `cat.Invoke` against stub `/things` handlers. The parity property —
  "the MCP skin returns the same result as the HTTP verb" — is asserted nowhere.
- **Rank rationale** — README headline (the answer to "an LLM will operate this
  platform") × the parity claim has zero coverage through real handlers.

### 3. The post-commit limb of the verify-before-run guard has no integration coverage

- **Capability** — verify-before-run: after an acquisition commits, the
  supervisor re-reads ownership before dispatching, so a claim stolen between
  commit and dispatch produces `orphaned_claim_lost_race` and **no** Execute.
  This is a **named load-bearing safety property** (README §1, §3).
- **Source claim** — README §1 ("verify-before-run" in the stable-safety list),
  §3; `concept:supervisor`, verify-before-run.
- **Value-delivering component** — the supervisor's post-commit verify-read
  guard (orchestration).
- **Proposed acceptance scenario** — Two concurrent `RunNode` callers race the
  same dispatch row against real Postgres; the loser commits its acquisition tx,
  then a forced ownership flip *between commit and the verify-read* must emit an
  `orphaned_claim_lost_race` event and make **no** Execute call at the real stub
  executor.
- **Why current coverage is nominal, not real** — `code:test/scenarios/verify_before_run_race_test.go::TestVerifyBeforeRunRace`
  exercises only the candidate-SELECT skip (`claimed_by IS NULL` filter); the
  test's own comment concedes the post-commit limb is "unit-tested in runtime
  (verifyBeforeRun is unexported)." A named safety guarantee proven only by an
  unexported-function unit test at the integration level.
- **Rank rationale** — explicitly-named stable safety property × the specific
  race it names is integration-uncovered.

### 4. The operator dashboard's auth gate and populated-state reads are untested

- **Capability** — the operator dashboard read endpoints (observability summary,
  event feed, frames, per-instance node state, dispatches), gated behind
  `observability:read`.
- **Source claim** — `concept:cascade-graph` ("operator-dashboard HTTP-route
  backplane"); README §4 lists observability as a primitive.
- **Value-delivering component** — the gated dashboard read over real runtime
  state (orchestration: the gate + the projection).
- **Proposed acceptance scenario** — Drive `route:GET /v1/observability/system/summary`
  (and the frames / event-feed / per-instance endpoints) through the real
  `code:lib/control/controlapi::NewApp`: (a) with no key → 401/403 from the real
  `observability:read` gate; (b) after seeding a real instance + node-runs +
  dispatches → the summary counts reflect the seeded state.
- **Why current coverage is nominal, not real** — `code:lib/control/observability/handler_test.go::TestHandler_SystemSummary_DispatchCounts`
  and `::TestHandler_ListFrames_Empty` mount a bare `chi.NewRouter()` with **no
  auth middleware** and assert response *shape* against a fresh empty DB. The
  production gate (`gateByAction("observability:read", …)`) is never exercised on
  this surface, and no populated-state read is asserted.
- **Rank rationale** — operability surface, moderately loud × the gate is
  bypassed and reads are shape-only against an empty DB.

### 5. The capability handshake's real probe-and-cache over the wire is untested

- **Capability** — at startup rimsky probes each service's observability gRPC
  protocol and caches its advertised capabilities (declared events,
  `expected_attributes_schema`) in the discovery cache; the cache feeds
  registration + dispatch validators.
- **Source claim** — `concept:observability`, `concept:discovery-cache`.
- **Value-delivering component** — the real `gRPCProber` dialing a real peer and
  filling the cache (orchestration).
- **Proposed acceptance scenario** — Run `RunHandshake` with the real
  `code:lib/control/observability::NewGRPCProber` against a loopback stub
  executor that advertises capabilities (declared events +
  `expected_attributes_schema`); assert `disc.GetExecutor(name)` returns
  Reachable with those caps cached; kill the stub, let `RefreshLoop` run, assert
  it flips to unreachable.
- **Why current coverage is nominal, not real** — `code:lib/control/observability/handshake_test.go::TestRefreshLoop_HealsUnreachable`
  and `::TestRunHandshake_UnreachableExecutor_NoError` use a `fakeProber`; the
  schema-resolver test populates the cache via `disc.SetExecutor` directly. The
  real probe→cache fill (the cache's reason to exist) is never driven over the
  wire — only the refresh-heal *policy* is. One test closes both `observability`
  and `discovery-cache`.
- **Rank rationale** — internal plumbing, lower loudness × the load-bearing leg
  (probe-over-wire) has only fake-prober coverage.

### 6. Role-template expansion → minted, usable, enforced key has no end-to-end test

- **Capability** — a CLI-bundled role JSON expands into a permission grant at
  key-creation time; the minted key then enforces exactly that grant.
- **Source claim** — `concept:role-template`; README §4 (per-key bearer tokens
  with permission grants).
- **Value-delivering component** — role expansion → real key mint → real gate
  enforcement (orchestration, with a pure-function expansion front).
- **Proposed acceptance scenario** — Mint a key over real `route:POST /auth/keys`
  with each bundled role's expanded grant; assert each role's representative
  action returns 200 and a non-role action 403 through the real gate.
- **Why current coverage is nominal, not real** — the cited proving tests are
  pure-function stand-ins: `code:cmd/rimsky/cli/roles/audit_read_coverage_test.go::TestRolesCoverAuditRead`
  calls `auth.CheckGrant` in-process; `code:cmd/rimsky/cli/auth_common_test.go::TestApplyGrantPatches_AddRemove`
  tests the CLI helper in memory. (`TestMCPSkin_OperatorRoleKeyWorks` covers an
  operator-shaped grant incidentally, so the gap is the *systematic* per-role
  mint-and-enforce.)
- **Rank rationale** — auth surface, moderate loudness × the end-to-end claim is
  proven only by pure-function helpers.

### 7. Sub-graph recursion/edge-case rejection is proven only at the validator layer

- **Capability** — registering a recursive (delegate-cycle) sub-graph template is
  rejected with `subgraph_recursion_unsupported`.
- **Source claim** — `concept:sub-graph`.
- **Value-delivering component** — registration-time rejection through the real
  `route:POST /templates` (orchestration).
- **Proposed acceptance scenario** — POST a delegate-cycle template to real
  `route:POST /templates`; assert HTTP 400 carrying `subgraph_recursion_unsupported`.
- **Why current coverage is nominal, not real** — `code:lib/graph/node/template_validator_graphs_test.go::TestCanonicalizeGraphs_RejectDelegateCycle`
  calls `ValidateTemplate` on an in-memory struct and asserts the error string;
  the happy path *is* route-tested (`code:test/scenarios/subgraph_exit_carry_e2e_test.go::TestSubgraphExitCarryE2E`)
  but the rejection path never goes through the real registration route.
- **Rank rationale** — narrow, low loudness × validator-unit-only (a small, well-
  understood closure — a candidate for going straight to `write-plan`).

### 8 & 9 — folded facets, listed for completeness

- **8. A real publisher never delivers into a real rimsky end-to-end.** Same gap
  as #1 viewed from the publisher side: `code:cmd/rimsky/conformance_publisher_test.go::TestPublisherConformance_FixtureCron`
  drives a *fixture* publisher into a *fake* receiver (`pubconformance.NewMessageReceiver`),
  not a real rimsky. Closing #1 with an image-driven harness closes this too.
  `concept:publisher`.
- **9. The claim-producer protocol's "real backing-state work" is only proven for
  the bundled postgres store's error vocabulary, not a real mutation.** The real
  bundled store *is* driven over the wire (`code:lib/services/test/scenarios/bundled_executor_vocab_test.go::TestPostgresStores_EmitsHierarchicalErrorClasses`),
  so the protocol contract is covered — but the only "real work" asserted is an
  error class, because the postgres store's `Commit`/`Abandon` are no-ops for
  scope-bytes claims (by design — see gap #10). Closed alongside #10.
  `concept:claim-producer`.

### 10. A real producer's stage-then-swap is never the value-delivering component in a held-subgraph end-to-end run

- **Capability** — held subgraphs commit-or-abandon atomically: on aggregate
  success the producer atomically swaps staged data into the canonical view; on
  any failure it drops the staging. README §2 ("Subgraphs that succeed or fail
  atomically … rimsky's atomic-staging primitive makes the all-or-nothing the
  default"); `concept:atomic-staging`.
- **Source claim** — README §2, §4 (held subgraphs); `concept:atomic-staging`.
- **Value-delivering component** — a *real* claim producer performing a real swap
  on Commit / drop on Abandon. This is a **component doing real work**. (The
  filesystem store's pick-policy mode is the shipped reference impl —
  `code:lib/services/stores/filesystem/store/store.go::Commit#257`, real `os.Rename`.)
- **Proposed acceptance scenario** — Drive a held subgraph whose claim is on the
  real **filesystem** pick-policy store, through a real rimsky stack: on
  aggregate-success the producer's real Commit **moves the staged folder to its
  committed location on disk** (assert the on-disk swap); on an injected
  aggregate-failure the real Abandon **drops the staging** (assert it's gone).
- **Why current coverage is nominal, not real** — the held-subgraph auto-terminal
  e2e (`code:lib/services/test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go`)
  points at the **postgres** store, whose `Commit`/`Abandon` are deliberate no-ops
  for scope-bytes claims, so it asserts the *dispatch* (production stays empty,
  staging survives) — never a real swap. The real filesystem swap *is* wire-tested
  in isolation (`fs_pick_policy_basic_test.go`) but never as the value-delivering
  component reached through rimsky's held-subgraph cascade. No swap to build, no
  doc to retract — just the missing end-to-end gate on the substrate where the
  swap is real.
- **Rank rationale** — README headline pattern ("atomic-staging makes all-or-
  nothing the default") × the real swap is never exercised end-to-end through the
  platform; the two halves (real dispatch, real swap) are each covered but never
  together.

---

## Drift symptoms — remove the false signal

### D1. ~~`atomic-staging` over-claims a swap reference impl~~ — RECLASSIFIED 2026-06-02 to coverage gap #10

> **Correction (grounded 2026-06-02).** This was wrong — it was derived from an
> audit of the *postgres* store only. The **filesystem** store *does* implement a
> real stage-then-swap: `code:lib/services/stores/filesystem/store/store.go::Commit#257`
> performs a real `os.Rename` swap via the pick-policy action, and it is exercised
> over the real gRPC wire by `code:lib/services/test/scenarios/stores/fs_pick_policy_basic_test.go::TestFsPickPolicy_BasicRingCycle`.
> The concept doc's "filesystem-substrate reference implementation" claim is
> **accurate**, and its Notes already concede the SQL-substrate producer-side
> lifecycle "is not yet shipped." So there is no over-claim and nothing to retract.
> The real residue is a **coverage gap** — the real swap is never the
> value-delivering component in a held-subgraph end-to-end run — now tracked as
> **coverage gap #10** below. The original (incorrect) drift analysis is retained
> for the audit trail.

- **The claim** — `concept:atomic-staging`'s Definition: "Producer-side
  stage-then-swap pattern: writers stage data into a side area; on commit the
  producer atomically swaps the staging into the canonical view; on abandon the
  staging is dropped." The concept's Boundaries claim ownership of "a reference
  implementation."
- **Cause** — over-claim. The bundled postgres store's `Commit`/`Abandon` are
  documented **no-ops** for scope-bytes claims (`code:lib/services/stores/postgres/store/store.go#274`);
  `Open` echoes the selector as the address without creating a staging schema.
  The proving test asserts the *opposite* of a swap — after Commit,
  `productionRowCount == 0` and `stagingSchemaExists == true`
  (`code:lib/services/test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go`).
  Only the verb dispatch and the verifier-executor pass/fail are real. The
  orchestration half (rimsky fires Commit/Abandon exactly once at subgraph end)
  *is* covered — by the `auto-terminal` and `parked-state` scenarios, not by the
  atomic-staging test. `VERIFICATION.md` already concedes the swap is
  "intentionally unbuilt."
- **Fix** — your call: **either** build a real stage-then-swap in a bundled store
  (so a real Commit moves staged rows into the canonical view and a real Abandon
  drops them, asserted by the test) — promoting this to a real gap — **or**
  retract the "atomically swaps … reference implementation" language from the
  `concept:atomic-staging` Definition/Boundaries so the concept describes what
  ships (verb dispatch + verifier pattern; swap deferred). `concept:asset` leans
  on this concept, so whichever you choose, check the asset doc reads true after.
- **Recommendation** — retract/clarify the concept doc unless the swap is on the
  near roadmap; it's pre-v1 and nothing in-tree exercises a real swap, so the
  documented reference impl is the louder falsehood.

### D2. `VERIFICATION.md` over-claims complete behavioral coverage

- **The claim** — `file:VERIFICATION.md` (committed 2026-06-02): "Every documented
  concept (70 of 71) is exercised by a test that drives the real running system
  and asserts an observable outcome," verdict **PASS**, "0 concepts shape-only or
  missing."
- **Cause** — over-claim. Under the stricter real-acceptance bar this report
  finds 9 genuine gaps, and several of VERIFICATION.md's own proving-test
  citations paper over them — citing shape-only, in-memory, fake-prober, or
  mis-attributed tests as "behavioral." Concrete mis-citations (the concept is
  often covered *elsewhere*, but the cited proof is weak):
  - `sensor` cited `TestTick_*` are in-process Tick-against-fake (no real
    sensor→cascade) — a true gap, not behavioral.
  - `observability` / `discovery-cache` cited tests use a `fakeProber` /
    `SetExecutor` — the probe-over-wire leg is untested.
  - `cascade-graph` cited handler tests bypass the auth gate and assert
    empty-DB shape.
  - `role-template` cited tests are pure-function (`CheckGrant`,
    `applyGrantPatches`).
  - `control-api` cited `TestRegistryCoversRouter` is a registry-map walk (no
    request reaches a handler).
  - `instance`'s second cited test (`TestConformancePostgres`) is a raw-SQL
    persistence suite, not an instance-route test.
  - `claim-co-holdership`'s `co_holding_drives_promotion_test.go` is proto-shape-
    only and its own comment admits the e2e harness was "deferred."
  - `frame`'s `…_DirectSQL` is a DB-constraint test, not an entry-point drive.
- **Fix** — regenerate `VERIFICATION.md` against the stricter bar once the gaps
  above close, **or** soften the verdict now (replace "every feature … PASS" with
  the honest split: ~61 really-covered, 9 acceptance gaps, 1 unbuilt reference
  impl) and re-point the weak citations at the test that *best* proves each
  concept (see the ledger below).
- **needs-your-call** — not set; this is a clear over-claim to correct, not an
  ambiguous gap-vs-non-feature.

---

## Covered (ledger)

Evidence the subtraction was done — the single test that best proves each
really-covered claim. **Not a list to maintain** (the tests are the source of
truth); regenerated each run.

| Concept | Best proving test |
|---|---|
| advisory-lock | `lib/graph/scheduler/scheduler_test.go::TestScheduler_AdvisoryLockBlocksSecondReplica` |
| anonymous-mode | `test/scenarios/auth/lifecycle_test.go::TestBootstrap_AnonymousToAuthenticated` |
| api-key | `test/scenarios/auth/lifecycle_test.go::TestRotation_DualActiveAndSweep` |
| asset | `lib/control/controlapi/assets_test.go::TestAssetEndpoints_DeleteReleasesAndDeletes` |
| attribute | `test/scenarios/attributes/substitution_dispatch_test.go::TestParamsSubstitutionAtDispatch` |
| auto-terminal | `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go::TestAutoTerminalAggregateCommitEndToEnd` |
| backfill | `lib/control/controlapi/backfills_test.go::TestBackfills_CreateRejectsFanOutNotWiredForOverride` |
| blob-backend | `lib/foundation/persistence/postgres/blob_largeobject_test.go::TestPgLargeObjectBackend` |
| breakpoint | `test/scenarios/breakpoints/resume_with_overlay_test.go` (overlay lands in real `ExecuteRequest.attributes`) |
| cancel-siblings | `test/scenarios/lineage/force_cancelled_lineage_test.go::TestForceCancelledLineage_CancelSiblingsEmitsForceCancelledRows` |
| cascade | `test/scenarios/cascade_invalidate_test.go::TestCascadeInvalidate` (not the cited message test) |
| claim | `test/scenarios/stores/scope_claim_test.go::TestScopeClaimEndToEnd` |
| claim-co-holdership | `test/scenarios/verifier/holds_only_auto_terminal_e2e_test.go::TestHoldsOnlyAutoTerminal` |
| claim-handle | `test/scenarios/asset/durable_lifetime_e2e_test.go::TestDurableLifetimeE2E` |
| claim-lifetime | `test/scenarios/asset/durable_lifetime_e2e_test.go::TestDurableLifetimeE2E` |
| claim-producer | `lib/services/test/scenarios/bundled_executor_vocab_test.go::TestPostgresStores_EmitsHierarchicalErrorClasses` (protocol contract; see gap #9) |
| claim-scope | `test/scenarios/locks/claim_scope_conflict_race_test.go::TestClaimScopeClaimRace_OneAcquirerWins` |
| claim-tree | `test/scenarios/forensics/fanout_post_mortem_test.go` |
| conformance | `cmd/rimsky/conformance_claimproducer_test.go::TestClaimProducerConformance_StubStore` (runner over real wire) |
| control-api | `test/scenarios/auth/lifecycle_test.go` (real 401/403/200 over the wire; not the cited registry tests) |
| data-processing | `test/scenarios/leaf_candidate_handle_e2e_test.go::TestLeafCarriesCandidateHandle` |
| delegation | `test/scenarios/subgraph_exit_carry_e2e_test.go::TestSubgraphExitCarryE2E` |
| dry-run | `test/scenarios/auth/dry_run_coverage_test.go::TestDryRunCoverage_AllWriteActions` |
| error-policy | `test/scenarios/retry_loop_cap_test.go::TestRetryLoopCapForcesGiveUp` |
| event-log | `test/scenarios/auth/audit_durability_test.go::TestAuditDurability_NoDropsUnderConcurrentLoad` |
| executor | `test/scenarios/agentic_executor_async_handoff_test.go::TestAgenticExecutorAsyncHandoff` (orchestration; stub peer) |
| fan-out | `test/scenarios/fanout_success_cascade_e2e_test.go::TestFanOutSuccessCascadeE2E` |
| frame | `test/scenarios/frame_resolution/serial_queue_each_invalidate_one_frame_test.go` |
| graph | `test/scenarios/subgraph_internal_cascade_e2e_test.go` |
| host-agent | `test/scenarios/host_agent_late_bind_executor_test.go::TestHostAgentLateBindExecutorHappyPath` (real spawned binary) |
| host-agent-proxy | `test/scenarios/host_agent_reap_test.go::TestHostAgentReapOnRunScopeTerminal` (real child SIGTERM) |
| inertness | `lib/foundation/persistence/blob_roundtrip_test.go::TestBlobRoundtripBackends` (positive side; negative is inherent non-coverage) |
| instance | `lib/control/controlapi/instances_test.go::TestTerminateInstance_ForceFailsRunningNode` |
| invalidate | `test/scenarios/cascade_invalidate_test.go::TestCascadeInvalidate` (not the cited envelope-shape test) |
| lifecycle-subscriber | `test/scenarios/lifecycle/lifecycle_e2e_test.go::TestLifecycleE2E_FullSequence` (6 callbacks over gRPC; 7th via host-agent reap) |
| lineage | `lib/services/subscribers/openlineage/subscriber_test.go::TestSubscriber_EndToEnd_PollsAndEmits` |
| lineage-record | `test/scenarios/lineage/claim_abandon_lineage_test.go::TestClaimAbandonLineage_NaturalAbandonEmitsAbandonedOutcome` |
| message | `test/scenarios/messages/message_cascade_e2e_test.go::TestMessageCascadeE2E_SubscriberFlipsStale` |
| module-layout | covered-by-lint — `.golangci.yml` depguard blocks + `go.work` graph at `make lint` (no runtime behavior) |
| named-event | `test/scenarios/conformance_events_test.go::TestConformanceEvents` |
| named-lock | `test/scenarios/locks/named_lock_limit_test.go::TestNamedLockSemaphoreSaturatesAtLimit` |
| node | `test/scenarios/cascade_invalidate_test.go::TestCascadeInvalidate` |
| node-run | `test/scenarios/locks/node_run_phase_test.go::TestNodeRunPhaseAdvancesOnClaim` |
| node-subscription | `test/scenarios/frame_coalesce_self_invalidate_test.go::TestFrameCoalesceSelfInvalidate` |
| orphan-reaper | `test/scenarios/orphaned_claim_test.go::TestOrphanedClaim` |
| parked-state | `test/scenarios/parked_lifecycle_test.go::TestParkedLifecycleResumeOnDeadline` |
| permission | `test/scenarios/auth/lifecycle_test.go::TestPermissionGrants_ReadOnlyDenyOnWrite` |
| persistence-database | `lib/foundation/persistence/conformance/conformance_test.go::TestConformanceSQLite` (full battery, both drivers) |
| publisher-subscription | `lib/control/controlapi/messages_test.go::TestCreateMessage_SenderKindPublisherWrongInstanceForbidden` |
| rimsky (CLI) | `cmd/rimsky/cli/instances_test.go::TestRunInstanceStatus_KeyResolution` (CLI-side; against a fake server — see note) |
| rimsky-yml | `lib/control/config/retired_aliases_test.go::TestLoadRimskyConfigYAML_RejectsRetiredAliases` |
| run-scope | `lib/foundation/persistence/conformance/run_scope_lifecycle.go::testRunScopeFanoutPartitionUniqueness` |
| service | `lib/services/test/scenarios/bundled_executor_vocab_test.go::TestPostgresStores_EmitsHierarchicalErrorClasses` |
| signal | `test/scenarios/breakpoints/signal_type_filter_test.go::TestSignalTypeFilter` |
| supervisor | `test/scenarios/verify_before_run_race_test.go::TestVerifyBeforeRunRace` (candidate-guard limb; post-commit limb is gap #3) |
| tag | `lib/control/controlapi/tags_test.go::TestDeleteTag_DoesNotDeleteTemplate` |
| template | `lib/control/controlapi/templates_test.go::TestTemplateRegister_Idempotent` |
| terminal-resolution | `test/scenarios/held_claim_acquirer_passes_test.go::TestHeldClaimAcquirerPasses` |
| transition-reason | `lib/control/controlapi/instances_test.go::TestTerminateInstance_ForceFailsRunningNode` |
| validation | `lib/control/controlapi/validation_pipeline_test.go::TestValidationPipeline_RejectsOnError` |
| wait-set | `test/scenarios/subscription_cascade_test.go::TestSubscriptionCascade_EligibilityRespectsMultipleSenders` |
| write-semantics | `test/scenarios/locks/write_semantics_coexistence_test.go::TestWriteSemanticsBlockingAsyncSerializesReaderBehindWriter` |

*Note on `rimsky` (CLI):* the CLI entry points are exercised against fake/in-
memory servers, never the real binary against a real control-api. Acceptable as
covered because the CLI is a declared thin pass-through, but a CLI-against-real-
server test would be the stronger proof (a minor, optional gap, not ranked).

---

## How to drive this backlog

Recovery is a campaign you drive item by item, highest rank first, in sessions
of your choosing. Per gap: take it into `/brainstorm` (or straight to
`/write-plan` for the simple, well-understood ones — #7 sub-graph rejection is a
good candidate) as a spec whose acceptance scenario is the one drafted above;
`write-plan` makes it the mandatory acceptance pass; `execute-plan` writes and
runs it against the real product, fixing any real bug it flushes out.

Per drift symptom: a small cleanup spec through the same path — D1 edits (or
builds behind) the `concept:atomic-staging` doc; D2 regenerates/softens
`VERIFICATION.md`. These are self-eliminating: once the words match reality, a
re-run of `/coverage` never re-proposes them.

Re-running `/coverage` anytime regenerates this report against current state —
including any acceptance passes added since. Convergence to an empty report
(no drift, shrinking gaps) is the goal.
