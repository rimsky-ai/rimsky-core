# Last-Mile Stability — Completion Report

Spec: `.ok-planner/specs/2026-06-11-last-mile-stability-design.md`
Plan: `.ok-planner/plans/2026-06-11-last-mile-stability.md`
Date: 2026-06-12

This report walks 100% of the spec's `## Manifest`: all 13 stories with
their proof artifacts, and all 39 technical decisions classified as kept
or diverged. The verification gate (build + test + lint) and the
no-deferral audit both passed before this audit ran.

---

## 1. Proof walkthrough

### STORY-subscription-mounting
Operator observes publisher subscriptions progress `mounting → active`
on the instance surface; no silent mount failure.

- **Artifact:** `examples/subscription-mounting-demo.sh`
- **Exhibits:** Boots `rimsky-all-in-one:latest` plus the bundled
  object-store sensor, then *pauses* the sensor container (the
  "publisher deliberately slow" condition). Registers a template with a
  `publishers:` block, creates an instance, prints the immediate 201,
  polls `GET /instances/{id}` showing the subscription in `mounting`,
  unpauses the sensor, shows the flip to `active` with no operator
  action, and tails the instance's messages showing sensor data
  arriving — the story's acceptance stages verbatim.
- **Invocation:** `make core-images service-images && bash
  examples/subscription-mounting-demo.sh`
- **Status:** EXHIBITS WORKING

### STORY-single-process-all-in-one
The all-in-one deployment runs all three roles in one OS process;
memory blobs are shared across roles and reaped.

- **Artifact:**
  `lib/services/test/scenarios/single_process_allinone_test.go`
  (`TestSingleProcessAllInOne_MemoryBlobAcrossRoles`)
- **Exhibits:** Boots the all-in-one image with the memory blob backend
  and a low spill threshold, asserts exactly one rimsky process serves
  all three role surfaces (no role children), drives a node to terminal,
  and round-trips an ~8.7 KiB spilled attribute across roles — the
  supervisor spills it, the control-api reads it back through the
  process-shared map — plus asserts the orphan-blob sweep runs clean.
- **Invocation:** `make core-images && cd lib/services && go test
  ./test/scenarios/ -run SingleProcessAllInOne -count=1`
- **Status:** EXHIBITS WORKING

### STORY-producer-error-passthrough
A producer's error class and message reach the API response under a
status distinguishing producer rejection from rimsky internal error.

- **Artifact:** `examples/producer-error-demo.sh`
- **Exhibits:** Boots all-in-one plus the filesystem store with a
  deliberately broken backing path, triggers the operation that makes
  the store reject, and prints the API response carrying the store's
  own error class and message under 502 (producer failed) vs 422
  (producer rejected input) vs 500 (rimsky internal) — the
  status-distinction the story demands.
- **Invocation:** `make core-images service-images && bash
  examples/producer-error-demo.sh`
- **Status:** EXHIBITS WORKING

### STORY-validation-names-the-mode
Reference-validation rejections name the active mode and the config key
that changes the behavior.

- **Artifact:**
  `test/scenarios/attributes/ref_validation_mode_e2e_test.go`
  (`TestAcceptance_RefValidationMode`)
- **Exhibits:** Registers a template with an unprovisioned executor
  reference through the real HTTP surface under strict mode (`all`) and
  asserts the rejection body names the active mode and
  `templates.ref_validation_mode` with its relaxed settings; the
  companion leg re-registers the same template under `available` and
  succeeds — proving the advice the error gives is true.
- **Invocation:** `go test ./test/scenarios/attributes/ -run
  TestAcceptance_RefValidationMode -count=1`
- **Status:** EXHIBITS WORKING

### STORY-all-upstream-gating
Fan-in receivers dispatch only after all in-flight upstreams in the
frame resolve, regardless of propagation path.

- **Artifact:** `test/scenarios/all_upstream_gating_test.go`
  (`TestAllUpstreamGating_DiamondSettlementPropagated`)
- **Exhibits:** Builds the A → (B, C) → D diamond with
  settlement-propagated staleness, holds C open deterministically,
  asserts D is not dispatch-eligible while C is in-flight, then releases
  C and asserts D dispatches exactly once with both B's and C's
  contributions in its substitution context. The strengthened
  `TestSubscriptionCascade_EligibilityRespectsMultipleSenders`
  (`test/scenarios/subscription_cascade_test.go:89`) pins the predicate
  alongside it.
- **Invocation:** `go test ./test/scenarios/ -run AllUpstreamGating
  -race -count=1`
- **Status:** EXHIBITS WORKING

### STORY-multi-hard-dep-rendezvous
Two-hard-dep shapes rendezvous: each upstream runs once, the receiver
runs once after both, the frame terminates.

- **Artifact:** `test/scenarios/multi_hard_dep_test.go`
  (`TestMultiHardDepRendezvous`) — written before the fix, per the
  test-first mandate. The reproduction **confirmed** the livelock
  (mutual re-affirmation of settled upstreams), recorded in
  `.ok-planner/design/decisions/hard-dep-settled-guard.md`; the test now
  stands as the regression pin over the settled-this-frame guard.
- **Exhibits:** Drives two independently-settling `hard_dep: true`
  upstreams in one frame; asserts each runs exactly once, the receiver
  runs exactly once after both, and the frame terminates.
- **Invocation:** `go test ./test/scenarios/ -run
  TestMultiHardDepRendezvous -race -count=1`
- **Status:** EXHIBITS WORKING

### STORY-producer-class-routing
Producer-declared classes route in `error_types:`; `acquire/*` keys
catch classified failures via the documented prefix fallback.

- **Artifact:** `test/scenarios/producer_class_routing_test.go`
  (`TestProducerClassRouting_ExactMatch`,
  `TestProducerClassRouting_PrefixFallback`)
- **Exhibits:** A stub producer declares `pg/claim_unavailable` in its
  capabilities; template A registers `error_types: {
  pg/claim_unavailable: retry }` successfully and routes a
  producer-classified acquisition failure to retry via exact match;
  template B declares only `acquire/unavailable: retry` and the same
  classified failure routes via the prefix fallback.
- **Invocation:** `go test ./test/scenarios/ -run ProducerClassRouting
  -count=1`
- **Status:** EXHIBITS WORKING

### STORY-validation-warnings-surfaced
Static-validator advisories appear in `validation_warnings`;
`warnings_as_errors=true` trips on them.

- **Artifact:** `test/scenarios/validation_warnings_test.go`
  (`TestValidationWarnings_StaticAdvisorySurfacedAndPromotable`)
- **Exhibits:** Registers a template that trips the acquisition-policy
  advisory through the real registration surface and asserts the
  advisory in `validation_warnings`; repeats with
  `?warnings_as_errors=true` and asserts rejection.
- **Invocation:** `go test ./test/scenarios/ -run ValidationWarnings
  -count=1`
- **Status:** EXHIBITS WORKING

### STORY-peer-tls-enforced
`tls: required` means a verified TLS connection or a loud failure
naming the peer and the mode.

- **Artifact:** `test/scenarios/peer_tls_test.go`
  (`TestPeerTLS_Required_VerifiedTLSEndToEnd`,
  `TestPeerTLS_Required_PlaintextPeer_LoudFailure`,
  `TestPeerTLS_Off_StaysPlaintext`)
- **Exhibits:** Dials a TLS-serving stub peer under `required` and
  exchanges a request end-to-end; dials a plaintext stub under
  `required` and asserts the failure names the peer and mode; the third
  leg pins that `off` remains plaintext.
- **Invocation:** `go test ./test/scenarios/ -run PeerTLS -count=1`
- **Status:** EXHIBITS WORKING

### STORY-commit-response-honored
Base-Commit `version_id` persists on the claim-handle row;
`producer_metadata` surfaces in the fan-out parent's writeback.

- **Artifact:** `test/scenarios/commit_response_fields_test.go`
  (`TestCommitResponseFields_PlainNode_VersionIDPersisted`,
  `TestCommitResponseFields_FanOut_ProducerMetadataInParentWriteback`)
- **Exhibits:** A stub producer stamps both fields on the base Commit
  response; the plain-node leg asserts the persisted `version_id` on
  the claim-handle row, the fan-out leg asserts the children's
  `producer_metadata` in the parent's writeback.
- **Invocation:** `go test ./test/scenarios/ -run CommitResponseFields
  -count=1`
- **Status:** EXHIBITS WORKING

### STORY-validation-mixin-uniform
Validation roles are honored identically for claim-producer, executor,
and publisher peers advertising the mix-in.

- **Artifact:** `lib/control/config/validation_mixin_uniform_test.go`
  (`TestValidationMixinUniformAcrossPeerKinds`)
- **Exhibits:** Stands up three stub peers (claim-producer, executor,
  publisher) each advertising the validation mix-in with the same
  roles, runs the registry dial
  (`DialPublisherAndValidationRegistries`), and asserts all three
  handshake-learned role sets are identical and non-empty.
- **Invocation:** `go test ./lib/control/config/ -run
  ValidationMixinUniform -count=1`
- **Status:** EXHIBITS WORKING

### STORY-work-completed-emitted
Every `work_started` event pairs with a `work_completed` carrying the
same identifiers plus the terminal kind.

- **Artifact:** `test/scenarios/work_completed_test.go`
  (`TestWorkCompletedPairsWorkStartedOnComplete`,
  `TestWorkCompletedPairsWorkStartedOnErrored`)
- **Exhibits:** Drives runs to complete and errored terminals through
  the real stack and asserts exactly one `work_started` / one
  `work_completed` pair per run with matching node/run identifiers and
  the terminal kind on the completion.
- **Invocation:** `go test ./test/scenarios/ -run WorkCompletedPairs
  -count=1`
- **Status:** EXHIBITS WORKING

### STORY-named-lock-metric
Named-lock acquisitions move a Prometheus counter distinguishable from
producer-claim acquisitions.

- **Artifact:** `test/scenarios/locks/named_lock_metric_test.go`
  (`TestNamedLockAcquisitionMovesLabeledMetric`)
- **Exhibits:** Drives a node acquiring a named lock through the real
  stack and asserts the acquisition counter moves with the named-lock
  labeling (acquired and unavailable outcomes both labeled).
- **Invocation:** `go test ./test/scenarios/locks/ -run
  NamedLockAcquisitionMovesLabeledMetric -count=1`
- **Status:** EXHIBITS WORKING

**Proofs: 13 / 13 exhibited. No GAPs.**

---

## 2. Technical decisions kept

Every TD in the manifest is enumerated here or in section 3.

1. **TD-harness-first-ordering** — harness lands before consolidations;
   child-execution last. Honored as executed: the race gates, injection
   hooks, and polling audit landed in Passes 1–3 before any
   consolidation, and the child-execution unification was the last
   consolidation (Passes 26–28). Recorded:
   `.ok-planner/design/decisions/harness-first-ordering.md`; the
   harness machinery they produced is cited under the next three TDs.
2. **TD-race-gate-split** — thin `-race -count=1` slice in `test-all`:
   `Makefile:72-73`; full `test-race` target with `-count=3`:
   `Makefile:79-81`; release chain requires it: `Makefile:283`.
3. **TD-race-injection-hooks** — deterministic hooks at the defended
   seams: `lib/runtime/runner.go:249-259` (`PreAcquireUnavailableHook`),
   `lib/runtime/runner.go:261-271` (`CheckAndFireHook`),
   `lib/runtime/orphan_reaper.go:72` (`PreReapHook`); driven by
   `test/scenarios/acquire_unavailable_abandon_injection_test.go:63`,
   `test/scenarios/held_claim_check_and_fire_race_test.go:50`,
   `test/scenarios/orphan_reaper_terminal_race_test.go:182,227`, and the
   post-fold pin
   `test/scenarios/verify_before_run_bail_engine_route_test.go:57` — all
   four spec seams covered.
4. **TD-polling-audit** — event-log-tail wait helper:
   `test/support/eventwait/eventwait.go` (append-only-ledger waits;
   seven test files converted to it); genuine outcome-waits left alone.
5. **TD-subscription-mounting-state** — `mounting` in the state set:
   `lib/foundation/persistence/postgres/migrations/009-subscription-mounting.sql`
   and the sqlite twin; constant
   `lib/foundation/persistence/publisher_subscriptions.go:31`; rows
   created in it `lib/runtime/publishers.go:114`; per-subscription state
   on the instance surface
   `lib/control/controlapi/instances.go:166-171,716-722`.
6. **TD-subscription-reconciler** — retry-forever worker:
   `lib/runtime/publishers.go:172-193`
   (`RunPublisherSubscriptionReconciler`, no attempt cap); started from
   the control-api `lib/control/config/controlapi.go:424`; `failed`
   reserved for non-retryable errors (unknown publisher name).
7. **TD-parallel-cap-removal** — both `-parallel 4` caps gone from
   `test-all`; the old Subscribe-flake comment replaced with the
   asynchronous-mounting note (`Makefile:61`).
8. **TD-claimant-guard-helper** — one written guard per driver:
   `lib/foundation/persistence/postgres/claim_handles.go:58-73`
   (`claimantGuard`, the single written predicate site) and
   `lib/foundation/persistence/sqlite/claim_handles.go:44-55`
   (`claimantGuardClause`); the claim-holder sites route through the
   same helpers (`postgres/claim_holders.go:177`,
   `sqlite/claim_holders.go:141`).
9. **TD-guard-conformance-suite** — wrong-claimant-is-a-no-op proven on
   both drivers:
   `lib/foundation/persistence/conformance/claimant_guard.go` (1032
   lines, one func per operation family), registered in
   `conformance.go::Suite` so both driver factories run it.
10. **TD-fold-ownership-bail** — the bail resolves through the unified
    engine: `lib/runtime/runner_acquire_postcommit.go:61`
    (`handleOrphanedClaim` calls `ResolveClaimHandleTerminal` with the
    `OwnershipBail` source, `lib/runtime/terminal_decision.go:72-82`);
    the hand-rolled verb-then-delete site is deleted; pinned by
    `test/scenarios/verify_before_run_bail_engine_route_test.go`.
11. **TD-acquire-unavailable-carveout** — single named carve-out:
    `lib/runtime/runner_lifecycle.go:70-72` (hook seam) with the
    carve-out rationale comment at ~line 280 (tx rolled back, no row
    delete); injection-tested by
    `test/scenarios/acquire_unavailable_abandon_injection_test.go`.
12. **TD-child-execution-unification** — one dispatch, one settlement:
    `lib/runtime/child_execution.go:170` (`DispatchChildren`) and `:357`
    (`SettleChildren`); `auto_terminal_chain.go`
    (`resolveParentClaimChain`) deleted outright; `CarryExitWriteback`
    deleted; delegation and fan-out are thin wrappers
    (`subgraph_dispatch.go:413,536`, `fanout_dispatch.go:289`); no
    schema change, template surfaces unchanged, fanout/subgraph suites
    green.
13. **TD-entry-absorption-flag** — `EntryAbsorbed bool` on the dispatch
    input: `lib/runtime/child_execution.go:126-132`, consumed at `:256`.
14. **TD-subclaims-as-input** — the primitive accepts already-acquired
    sub-claims (`SubClaimHandleID`,
    `lib/runtime/child_execution.go:81-86`) and never calls the
    producer's split itself (stated at `:22`); `AcquireSubClaims`
    unchanged.
15. **TD-carry-verbatim-requires-one** — see section 3 (enforcement
    site diverged; the invariant itself holds).
16. **TD-cascade-inside-settlement** — the parent-settlement cascade
    bridge fires inside the primitive:
    `lib/runtime/child_execution.go:495-529`
    (`cascadeSubscribersStaleInTx` called within `SettleChildren`'s
    settlement transaction).
17. **TD-child-execution-naming** — `DispatchChildren` /
    `SettleChildren`, exactly as specified
    (`lib/runtime/child_execution.go:170,357`).
18. **TD-upstream-gating-at-eligibility** — the propagation-path-
    independent eligibility condition:
    `lib/runtime/runner_acquire_upstream_gate.go`
    (`candidateGatedByInFlightUpstream` at the pre-claim chokepoint,
    `@blessed-invariant` block in the file header; self-edges excluded);
    persistence half `Queue.AnyInFlightRunForNodes`
    (`lib/foundation/persistence/node_runs.go:183-196`,
    `postgres/queue.go:725`, `sqlite/queue.go:808`, parity-tested in
    `conformance/any_in_flight_for_nodes.go`); wait-set substitution
    role untouched (no rows seeded); strengthened multi-sender pin
    `test/scenarios/subscription_cascade_test.go:89`.
19. **TD-hard-dep-settled-guard** — test-first honored: the
    reproduction (`test/scenarios/multi_hard_dep_test.go`) confirmed the
    livelock; the settled-this-frame guard landed in
    `lib/runtime/runner_terminal.go::pullHardDepUpstreams` (~line 999;
    settled-upstream re-affirmation skipped, in-flight probe first); the
    confirmation is recorded in
    `.ok-planner/design/decisions/hard-dep-settled-guard.md`.
20. **TD-sweep-lock-skip-on-error** — lock error treated as lock-held:
    `lib/graph/scheduler/scheduler.go:255-266` (warn + `return nil`,
    never run unlocked); unit-tested in `scheduler_test.go`; invariant
    added to `concepts/advisory-lock.md`.
21. **TD-parity-expansion** — driver-parity suite extended with
    `frame_lifecycle.go`, `frame_settlement.go`, `park_resume.go`,
    `retention_sweep.go`, `message_idempotency.go`,
    `claim_handle_queries.go`, `publisher_subscriptions.go`, and
    `any_in_flight_for_nodes.go` under
    `lib/foundation/persistence/conformance/`, all registered in
    `Suite` and run against both drivers.
22. **TD-sqlite-multiproc-safety** — both halves: bare read-then-write
    sites are transactional via BEGIN IMMEDIATE
    (`lib/foundation/persistence/sqlite/nodes.go:300`,
    `node_attributes.go:218`, `api_keys.go:48,183`; pool comment in
    `database.go` updated); tick + migration locks are flock(2)-based
    file locks (`sqlite/advisory_locker.go:19-55`,
    `<db>.tick.lock` / `<db>.migrate.lock`, cross-process), tested in
    `advisory_locker_test.go`. No startup gate, per the tension
    resolution.
23. **TD-single-process-mode** — the no-command entrypoint path runs
    migrate synchronously then all three roles in one process:
    `cmd/rimsky-entrypoint/main.go:125-176` (sets
    `RIMSKY_PROCESS_ROLE=unified` only there; `launch.RunScheduler` /
    `RunSupervisor` / `RunControlAPI` in-process, one signal-handled
    shutdown); single-role spawns unchanged and deliberately NOT marked
    unified (`main.go:236`). See section 3 for the `lib/control/launch`
    package this necessitated.
24. **TD-memory-gate-premise-corrected** — gate retained with a true
    premise: `lib/foundation/persistence/blob_config.go:125-126`
    (memory rejected unless `RIMSKY_PROCESS_ROLE=unified`), error text
    and comments (`:84-106`) now describe the single-process mode.
25. **TD-topology-test-coverage** — both topologies integration-tested:
    `lib/services/test/scenarios/single_process_allinone_test.go:57`
    and `lib/services/test/scenarios/split_topology_test.go:26`
    (three-container split over shared Postgres, driven to terminal).
26. **TD-producer-error-passthrough** — typed producer errors cross the
    boundary: `lib/runtime/peer/errors.go` (`ProducerCallError`
    carrying `error_class` via `google.rpc.ErrorInfo`);
    `lib/control/controlapi/app.go:345-392` (`writeProducerError`: 422
    producer-rejected-input vs 502 producer-failed, body carries
    `producer_name` + `error_class` + message); unit-tested in
    `app_producer_error_test.go`.
27. **TD-validation-error-names-mode** — rejection text built once:
    `lib/graph/node/template_validator.go:104-127`
    (`RefValidationMode.String` + `refValidationModeRejection` naming
    the mode and `templates.ref_validation_mode` with relaxed settings),
    used by all four reference legs.
28. **TD-producer-declared-classes-capability** —
    `lib/protocols/proto/v1/claim_producer.proto:138`
    (`repeated string declared_error_classes = 6`, comment mirroring the
    executor-observability field); stored in the discovery cache at
    handshake (`lib/control/observability/discovery.go:94-108`).
29. **TD-validator-learns-producer-classes** — `validateErrorTypes`
    range-checks against the executor ∪ `acquire/*` ∪
    reachable-producer union
    (`lib/graph/node/template_validator.go:186-192,475-483`);
    unattributable keys become advisory warnings (`res.Warnings`), never
    hard rejections.
30. **TD-acquire-prefix-fallback** — exact producer class falls back to
    the `acquire/*` family before the unknown-class default:
    `lib/runtime/on_error.go:453-474` (`lookupPolicy`, fallback order
    documented at the lookup site; exact key always wins); unit-tested
    in `on_error_fallback_test.go`.
31. **TD-merge-validator-warnings** — static warnings merged into both
    responses and the `warnings_as_errors` gate:
    `lib/control/controlapi/templates.go:232-284`
    (`staticWarningsToFindings` merged with pipeline warnings, feeding
    `validation_warnings` and the rejection gate in both the register
    handler and the validate endpoint).
32. **TD-wire-commit-response-fields** — the producer client returns
    the Commit response body (`lib/runtime/peer/client.go:91-105`); the
    engine persists base-Commit `version_id` claimant-guarded
    (`lib/runtime/terminal_decision.go:269-276` via `SetVersionID`);
    `producer_metadata` threads into `SettleChildren` for the fan-out
    parent writeback (`terminal_decision.go:263,319`).
33. **TD-plumb-validation-roles** —
    `lib/protocols/proto/v1/executor_observability.proto:79`
    (`repeated string validation_supported_roles = 9`);
    `DialPublisherAndValidationRegistries` resolves live roles for all
    three peer kinds via per-kind `fetchRoles`
    (`lib/control/config/publishers.go:130-237`), executor roles riding
    the observability handshake, publisher roles from
    `PublisherCapabilitiesResponse`.
34. **TD-peer-tls-enforcement** — shared credentials helper
    `lib/runtime/peer/credentials.go:59` (`TransportCredentials`:
    `required` → verified TLS against system roots, else plaintext);
    honored at every enumerated dial site (`peer/dial.go`,
    `peer/publisher_client.go`, `peer/data_processing_client.go`,
    `peer/validation_client.go`, `executor/client.go`,
    `lib/control/observability/handshake.go`); the `tls` key now parses
    on store and publisher entries as well
    (`lib/control/config/stores.go:434-494`); failures name the peer
    and mode.
35. **TD-tls-mode-validation** — validated enum:
    `lib/control/config/stores.go:689-704` (`parseTLSMode`: exactly
    `"" | off | required`; `optional` and anything else is a config
    error naming the entry); tested in
    `lib/control/config/tls_mode_test.go`.
36. **TD-emit-work-completed** — `lib/runtime/runner_terminal.go:127,
    131-144` (`emitWorkCompleted` appends the kind at terminal
    application with the `work_started` identifiers plus the terminal
    kind; parked/await-async re-entry correctly excluded).
37. **TD-named-lock-metric** —
    `lib/runtime/runner_acquire_named_locks.go:90,117`
    (`IncNamedLockAcquisition(name, "unavailable"|"acquired")`, a
    labeled counter distinct from producer-claim acquisitions, noop +
    Prometheus implementations updated).
38. **TD-comment-drift-sweep** — all ten enumerated sites corrected:
    `lib/control/controlapi/mcp_route.go` (route comment now `/v1/mcp`),
    `lib/protocols/publisherkit/publisher.go` (godoc route + retry
    arithmetic), `lib/runtime/publishers.go` (retry comments rewritten
    with the reconciler), `lib/services/executors/http-node/server.go`
    (`parseRetryAfter` doc-comment, `:88,236-248`),
    `lib/runtime/runner_terminal.go` (wait-set comment),
    `lib/control/config/stores.go` (plain-language error text),
    `feature-index.md` (stale rows).
39. **TD-delete-archived-author-guide** —
    `.ok-planner/archive/internal/claim-producer-author-guide.md`
    deleted (git-tracked deletion in the working tree).

**Kept: 38 of 39.**

---

## 3. Technical decisions diverged

### TD-carry-verbatim-requires-one (spec TD — diverged)
- **Restatement:** carry-verbatim requires exactly one child, enforced
  at template canonicalization.
- **Spec said vs. implemented:** the spec located the N=1 check at
  template canonicalization. The implementation enforces it at template
  validation instead: delegation declares exactly one child by
  syntactic construction (the `delegate:` surface cannot express more),
  so the only reachable multi-child carry-verbatim shape is a `fan_out:`
  declaring `error_policy.kind: carry_verbatim` — rejected at
  validation with class `carry_verbatim_requires_single_child`
  (`lib/graph/node/template_validator_holds.go:178-190`); the policy
  enum documents the constraint
  (`lib/foundation/spec/aggregation_policy.go:35-41`); tested in
  `template_validator_holds_test.go:238`.
- **Flavor:** improved.
- **Reason:** the checked invariant the TD wanted ("a delegation that
  somehow declares multiple children is a template error") holds, and
  rejecting at validation surfaces the error to the operator in the
  registration response with a named rejection class, earlier than a
  canonicalization-time failure would.

### Importable role runners — `lib/control/launch` (necessitated, not a spec TD)
- **Restatement:** the single-process entrypoint needed in-process role
  entry points whose behavior is byte-for-byte the role binaries'.
- **What was implemented:** a new `lib/control/launch/` package
  (`scheduler.go`, `supervisor.go`, `controlapi.go`) wrapping the
  `config.Start*` calls *plus* the role mains' surrounding wiring
  (metrics refreshers, the supervisor's observability handshake,
  background loops), returning stop handles; the three role mains shrink
  to thin shells over `launch.Run*`.
- **Flavor:** necessitated.
- **Reason:** TD-single-process-mode named `config.StartScheduler` /
  `StartSupervisor` / `StartControlAPI` as the entry points, but the
  role mains carried load-bearing wiring beyond those calls; calling
  the bare `Start*` functions in-process would have made the unified
  mode behave differently from the role binaries. The launch package is
  the single shared shape both invocations use, so
  STORY-single-process-all-in-one's "single-role deployments behave
  exactly as today" acceptance holds.

### HTTP-bridge executor TLS coverage (necessitated, not a spec TD)
- **Restatement:** `tls: required` must be honored on every executor
  dial, including the HTTP-transport bridge the spec's gRPC dial-site
  enumeration did not name.
- **What was implemented:** `required` on an HTTP-transport executor is
  enforced at config parse time — the endpoint must be `https://`
  (`lib/control/config/stores.go:223-224`) — and the HTTP client honors
  verified TLS (`lib/runtime/executor/client_http.go`, tested in
  `client_http_test.go`), with a `SetTLSRootCAsForTesting` seam in
  `lib/runtime/peer/credentials.go:43-47` (production default stays
  system roots, as the plan sanctioned).
- **Flavor:** necessitated.
- **Reason:** STORY-peer-tls-enforced's falsifier is "the key accepted
  and silently ignored"; an executor entry with `transport: http` can
  carry the same `tls` key, so leaving the bridge out would have
  manufactured exactly the false confidence the TD exists to remove.

**Diverged: 1 spec TD + 2 necessitated implementation choices.**

---

## Coverage check

- **Stories:** 13 exhibited / 13 in manifest. No GAPs; no process
  defect.
- **Technical decisions:** 38 kept + 1 diverged = 39, matching the
  manifest's 39 TDs. No silent attestations; every TD enumerated.
- **Design changes (cross-checked):** 3 tensions resolved and moved to
  `_resolved/`; `concepts/child-execution.md` created; 13 concepts
  mutated; 13 story files and 39 decision files created;
  `concepts.md` / `stories.md` / `decisions.md` catalogs refreshed —
  all matching the manifest's design-changes section.
- **Mismatches:** none.
