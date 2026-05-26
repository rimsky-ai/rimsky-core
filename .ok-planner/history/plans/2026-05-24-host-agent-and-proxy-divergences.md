# Divergence record — 2026-05-24-host-agent-and-proxy

Auditor's note: this is an honest record of where the working tree differs from
what the plan literally said, so the user knows what creative choices were made.
It is NOT a critique — correctness review is a separate step. Each entry was
verified against the actual tree (not taken on faith from the implementer's
flags). The 10 implementer-flagged divergences are all confirmed; several
additional ones surfaced during the audit and are recorded below.

The implementation tracks the plan very closely. Most passes landed
substantially verbatim. The divergences below are the meaningful deltas a
reviewer would want to know about.

---

## 1. Proto liveness/error messages renamed to avoid flat-namespace collision (Pass 1)

- **Plan said:** `host_agent.proto` Task 1 — verbatim message names `Heartbeat`,
  `HeartbeatAck`, and `Error`.
- **Implemented:** `protocols/proto/v1/host_agent.proto:64-65,92,96,135` renames
  them to `HostAgentHeartbeat`, `HostAgentHeartbeatAck`, and `HostAgentError`
  (referenced from `SpawnAck.error`, `Reaped.error`, and the frame oneofs).
- **Inferred reason:** Forced choice. All v1 protos share the flat `rimsky.v1`
  package, and `executor.proto` already owns `Heartbeat` / `Error`. The
  verbatim names from the plan would have been a hard protoc compile error.
  Comments in the proto explain the rename. (Implementer-flagged; verified.)

## 2. Admit-list SQL parameters numbered `$4`/`$5`, not the plan's `$3`/`$4`; SQLite cols typed TEXT (Pass 2)

- **Plan said:** Task 13 — late-bind proxy params become `$3` (executor proxy)
  and `$4` (claim-producer proxy).
- **Implemented:** `foundation/persistence/postgres/queue.go:227-258` — the
  existing query already used `$3` for `LIMIT`, so the new params are bound as
  `$4` (executor proxy) and `$5` (claim-producer proxy). The OR-branch guards
  are `$4 <> ''` / `$5 <> ''`. SQLite `service_bindings` and
  `created_by_api_key_id` columns are typed `TEXT`
  (`foundation/persistence/sqlite/migrations/002-host-agent-proxy.sql:11-12`),
  matching the file's UUID-as-TEXT / JSON-as-TEXT convention.
- **Inferred reason:** Plan error (off-by-the-existing-`$3`). The plan's SQL
  intent is preserved exactly; only the placeholder numbering shifted to make
  room for the pre-existing `LIMIT $3`. (Implementer-flagged; verified.)

## 3. `Registry.GetWithContext` takes a plain `instanceID string`, carrying a `@diverged` annotation (Pass 3)

- **Plan said:** Spec §"Dispatch resolution" sketched
  `GetWithContext(name string, ctx DispatchContext)`. Task 16 then directed the
  plain-`instanceID` form because foundation cannot import runtime.
- **Implemented:** `foundation/locks/registry.go:140` —
  `GetWithContext(name string, instanceID string)` with a `@diverged: true`
  block (lines 135-139) recording the layer-purity rationale.
- **Inferred reason:** Forced choice (layer-purity / `foundation-purity`
  depguard forbids `foundation/` importing `runtime/`'s `DispatchContext`).
  Plan and spec disagreed; Task 16 resolved it the way it landed. (Both
  implementer-flagged and plan-directed; verified.)

## 4. Terminal-resolution `Get` sites left bare (no instance context) (Pass 3)

- **Plan said:** Task 20 — sweep `Registry.Get` dispatch-path callers; switch to
  `GetWithContext` where `InstanceID` is available, leave startup/sweep callers
  bare.
- **Implemented:** `runtime/auto_terminal_chain.go:193` leaves
  `args.StoreRegistry.Get(producerName)` bare with an explanatory comment
  ("Terminal-resolution path (not dispatch-time acquisition)… no instance
  context is threaded into the recursive resolution walk").
- **Inferred reason:** Cleaner shape consistent with the plan's own guidance —
  the parent claim was already bound at acquire time, so late-bind resolution is
  not needed on the terminal-resolution walk. (Implementer-flagged; verified.)

## 5. Service-name interceptor homed in `runtime/peer`, not `runtime/clientiface` (Pass 4)

- **Plan said:** Task 21 — create the interceptor at
  `runtime/clientiface/service_name_interceptor.go`, with a hedge to relocate if
  `clientiface` doesn't own gRPC plumbing.
- **Implemented:** `runtime/peer/service_name_interceptor.go` (package `peer`).
  The file header explains `clientiface` is a deliberately gRPC-free DTO
  surface. Consequence: `runtime/executor/client.go:17` now imports
  `runtime/peer` to reach `peer.ServiceNameUnaryInterceptor` /
  `ServiceNameStreamInterceptor`.
- **Inferred reason:** Cleaner shape (and the plan explicitly licensed the
  relocation). The new executor→peer import is a fresh intra-`runtime/` edge the
  plan didn't call out, but it stays within the `runtime/` layer. (Implementer-
  flagged; verified.)

## 6. Only the claim-producer `Open` path routes through `applyErrorPolicy`; Commit/Abandon/Release stay on the existing terminal path (Pass 4)

- **Plan said:** Task 26 — route claim-producer errors through
  `applyErrorPolicy` at the `Open` failure path "and the analogous
  `Commit`/`Abandon`/`Release` call sites."
- **Implemented:** Only `Open` gets the new policy routing: `acquireClaim`
  (`runtime/runner_acquire_claims.go:106-127`) returns a new
  `openResultErrored` + `*peer.ProducerCallError`; `tryAcquire`
  (`runtime/runner_acquire.go:547-578`) builds a partial `acquisition` and a new
  `errAcquireProducerErrored` sentinel; `acquireCandidate` dispatches it to a
  new `handleAcquireProducerError`. Commit/Abandon/Release still translate via
  `*peer.ProducerCallError` (`runtime/peer/client.go:94-138`) but remain on the
  existing policy-aware terminal path, carrying the translated class without a
  new `applyErrorPolicy` call site.
- **Inferred reason:** Cleaner shape / minimal disturbance. `Open` is the only
  one that fails *during acquisition* (before an `acquisition` exists), so it
  needed the new sentinel + partial-acq plumbing; the terminal verbs already run
  inside a policy-aware path. (Implementer-flagged; verified.)

## 7. go.mod / go.sum left "untidy"; broader dependency churn than a clean tidy (Pass 4)

- **Plan said:** Task 25 adds a direct `google.golang.org/genproto/.../errdetails`
  import. (No explicit instruction to run `go mod tidy`.)
- **Implemented:** `go.mod`/`go.sum` show: `genproto/googleapis/rpc` promoted
  from indirect to direct; new direct `golang.org/x/term` (for `auth login`
  password prompt); `genproto/googleapis/api` + `cel.dev/expr` +
  `antlr4-go/antlr/v4` + `google/cel-go` + `golang.org/x/exp` + `go.yaml.in/yaml/v3`
  pulled in (indirect); `robfig/cron/v3` dropped from direct require;
  `testcontainers-go` (+ postgres module) demoted from direct to indirect;
  several test-dep hashes (`go-spew`, `go-difflib`) bumped to pseudo-versions.
- **Inferred reason:** The implementer flagged that a full `go mod tidy` would
  "churn unrelated deps," yet the committed `go.mod`/`go.sum` already carry a
  noticeable amount of that churn (cron removed, testcontainers demoted, CEL/antlr
  graph pulled in). This looks like a partial/transitive tidy rather than the
  surgical add the flag implies. Worth a reviewer's eye to confirm the
  build-graph changes are intended and not an accidental over-tidy. (Partially
  implementer-flagged; the breadth of churn is an auditor finding.)

## 8. `LateBindServices` field uses `omitempty`; `LateBindServiceProxies` threaded through `ControlAPIConfig` (Pass 5)

- **Plan said:** Task 27 — add `LateBindServices []string` to `TemplateSpec`.
  Task 29 — add `LateBindServiceProxies` to `AppDeps`, wired from config.
- **Implemented:** `foundation/spec/template.go:58` tags the field
  `yaml:"late_bind_services,omitempty" json:"...,omitempty"` so existing
  templates (no `late_bind_services`) canonicalize byte-identically and keep
  their content-address hashes. The proxies map is threaded via a new
  `ControlAPIConfig.LateBindServiceProxies` field
  (`control/config/controlapi.go:101`) and passed at the `StartControlAPI` call
  site into `AppDeps` (line 327-329) — a config-struct hop the plan named only
  at the `AppDeps` end.
- **Inferred reason:** Cleaner shape + invariant preservation (`omitempty`
  protects `concept:template` content-addressing for unaffected templates). The
  `ControlAPIConfig` threading is the natural production wiring path. (Implementer-
  flagged; verified.)

## 9. Both main-scope close sites wrapped in `Persist.Transaction(...)` to avoid a PANIC (Pass 6)

- **Plan said:** Task 35 — `deps.Persist.RunScopes().Close(ctx, nil, inst.MainRunScopeID)`
  (nil tx) at the terminator tick and DELETE sites.
- **Implemented:** Both sites wrap the close in `Persist.Transaction(...)`:
  `control/controlapi/instance_terminator.go:170-178` and
  `control/controlapi/instances.go:647-661`. The Table API requires an explicit
  tx (passing `nil` would panic). The terminator-test ordering assertion was
  updated to expect two lifecycle calls in sequence
  (`control/controlapi/instance_terminator_test.go:133-156`:
  `on_run_scope_terminal` before `on_instance_terminated`).
- **Inferred reason:** Plan error (the `nil`-tx snippet would crash). The DELETE
  site uses the import alias `foundationshared.UUID` rather than `shared.UUID`
  — a trivial naming difference dictated by that file's existing imports.
  (Implementer-flagged; verified.)

## 10. Lifecycle fan-out threaded through `PropagationArgs`; fired at the two supervisor close sites (Pass 7)

- **Plan said:** Task 37 option (a) — extend `PropagationArgs` with `Persist`,
  `LifecycleSubs`, `LifecyclePeersForSpec`; fire at the sub-graph and
  fanout-partition close sites.
- **Implemented:** As planned. `runtime/state_propagation.go:105-114` adds the
  three fields; the fan-out fires at
  `runtime/subgraph_dispatch.go:263-281` (`CarryExitWriteback`, reason
  `"subgraph_exit"`, instance resolved via `exitScope.InstanceID`) and
  `runtime/auto_terminal_chain.go:161-180` (`resolveParentClaimChain`, reason
  `"fanout_partition_terminal"`, instance via `childScope.InstanceID`). The
  fan-out is guarded `if args.Persist != nil` and treats lookup misses as
  no-ops so the close never rolls back on a fan-out resolution failure.
- **Inferred reason:** Matches the plan's chosen option (a). The `args.Persist != nil`
  guard at the subgraph site and the per-site instance-id source variable
  (`exitScope`/`childScope`) are sensible local adaptations. (Implementer-
  flagged; verified.)

## 11. Claim-producer write-semantics envelope served on `ClaimProducer.Capabilities`, plus a claim-route map; spawn-dedup keyed on instance-id (Pass 8)

- **Plan said:** Task 45 — implement `ClaimProducerObservability.Capabilities`
  to advertise `realized_write_semantics: [SYNC, STAGED_ASYNC, BLOCKING_ASYNC,
  READ_ONLY]`. Spec §"Spawn lifecycle" — one spawn per `(run_scope_id,
  binding_name)`.
- **Implemented (three sub-divergences):**
  - **Write-semantics envelope.** It is served on the protocol's own
    `ClaimProducer.Capabilities` (`cmd/rimsky-host-agent-proxy/claim_producer_handler.go:56-63`,
    via `CapabilitiesResponse.WriteSemanticsAllowed`), NOT on the observability
    handler — `ClaimProducerObservability.Capabilities` returns an empty
    `ClaimProducerObservabilityCapabilities{}` (line 247-248). The observability
    gen message has no write-semantics field, so the envelope had nowhere to go
    there.
  - **Claim-route map.** A `claimRoutes map[string]claimRoute` (claim_id →
    api_key_id + spawn_id) was added (`state.go:32,397-414`) so Commit / Abandon
    / Release — which carry only `claim_id` on the wire — can route back to the
    spawned producer recorded at `Open` time (`claim_producer_handler.go:99,105,126,147`).
    The plan didn't describe this index.
  - **Spawn-dedup keyed on instance-id, not run-scope-id.** The dedup index
    (`runScopeBindingKey`) is keyed on `scopeID`, and `scopeID = instanceID`
    (`dispatch.go:131`, `state.go:210` comment "the dispatch-observable scope
    (instance id in v1)"). The wire requests (`ExecuteRequest` / `OpenRequest`)
    carry `instance_id` but not `run_scope_id`, so the implementer used the
    instance id as the spawn scope.
- **Inferred reason:** Forced choices driven by what the existing protos
  actually carry on the wire (no write-semantics field on the obs message; no
  `run_scope_id` on Execute/Open; no producer-name on Commit/Abandon/Release).
  (Implementer-flagged; verified.)

## 12. Consequence of #11: `OnRunScopeTerminal` reap key does not match the spawn-dedup key (auditor finding, Pass 6 + Pass 8 interaction)

- **What the design said:** Spec §"Reap" — `OnRunScopeTerminal` fires with a
  `run_scope_id`; the proxy looks up all spawn-ids associated with that
  `run_scope_id` and reaps them.
- **What was implemented:** The reap path
  (`cmd/rimsky-host-agent-proxy/lifecycle_handler.go:52-53`) calls
  `state.dropSpawnsForRunScope(req.GetRunScopeId())`, which matches
  `spawnState.scopeID == req.RunScopeId` (`state.go:337-352`). But per #11,
  `scopeID` is the **instance id**, while the firing sites pass a **run-scope
  id**: control-api passes `inst.MainRunScopeID`
  (`instance_terminator.go:177-178`, `instances.go:660-661`) and the supervisor
  passes the sub-graph / partition `RunScopeID` (`subgraph_dispatch.go:277`,
  `auto_terminal_chain.go:171`). Those ids never equal the instance id, so
  `dropSpawnsForRunScope` finds nothing and the reap is a no-op on this path —
  spawns are then cleaned up only via agent disconnect / displacement
  (`dropAgent`, `state.go:259-275`).
- **Inferred reason:** The two halves of the spawn-lifecycle were resolved
  independently: dedup adopted the on-the-wire `instance_id` (#11) while reap
  kept the design's `run_scope_id` event key. The mismatch is an emergent
  consequence, not a flagged choice. Recorded here for the correctness review;
  no fix proposed. (Auditor finding — surfaced during verification, not in the
  implementer's flag set.)

## 13. Claim-producer verb inferred from request shape; client-side dirs classified Apache (Pass 9)

- **Plan said:** Task 49 `dispatch.go` — deserialize `frame.Payload` based on
  `frame.Protocol` and forward via the gRPC client.
- **Implemented:** `runtime/hostagent/dispatch.go:97-138` — because the wire
  `DispatchFrame` carries only `protocol` (no method verb), the agent infers the
  claim-producer verb from the request *shape*: it tries `OpenRequest`
  (carries `claim_id` + `producer_name`); Commit/Abandon/Release are
  wire-compatible at the `claim_id` field and are forwarded as a `Commit`
  (lines 126-134, with a comment noting the wire-compat shortcut).
  `licensing.yml:33,47` classifies `runtime/hostagent/` and
  `cmd/rimsky-host-agent/` as Apache (client-side, bundled into the CLI).
- **Inferred reason:** Forced choice (no verb on the wire) + deliberate
  license carve-out matching the existing client-side Apache set. (Implementer-
  flagged; verified.)

## 14. Proxy dir + migration-002 classified AGPL; concept bodies rewritten path-free (Pass 10)

- **Plan said:** Task 56/57 — write concept bodies path-free per the
  self-containment rule; the spec's New-concepts text leaked some symbols
  (`exec()`, `$PATH`, conformance-binary names).
- **Implemented:** `licensing.yml:88` classifies
  `cmd/rimsky-host-agent-proxy/` AGPL (platform-side service binary); the two
  migration-002 SQL files carry AGPL headers
  (`postgres/migrations/002-host-agent-proxy.sql:1-3`,
  `sqlite/migrations/002-host-agent-proxy.sql:1-3`). Concept bodies under
  `.ok-planner/design/concepts/` were transcribed with the spec's leaked symbols
  rewritten to prose/role descriptions (verified the new
  `host-agent.md` / `host-agent-proxy.md` and the 11 mutated concept files carry
  no file paths or code citations in their bodies).
- **Inferred reason:** Spec-intent override (self-containment rule wins over the
  spec's verbatim leaked symbols) + correct license classification per the
  platform/client split. (Implementer-flagged; verified.)

## 15. Real bug fixed: `GET /instances/{id}` now exposes `service_bindings` + `created_by_api_key_id` (Pass 10)

- **Plan said:** Spec §v1 scope — the proxy's cache-miss fallback reads
  `GET /instances/{id}`. The plan never explicitly listed extending the GET
  response struct.
- **Implemented:** `control/controlapi/instances.go:146-150,173-178` extends
  `instanceItem` (the GET response struct) with `service_bindings` and
  `created_by_api_key_id` (both `omitempty`), populated in `toInstanceItem`.
  Without this the proxy's GET-fallback fetcher
  (`cmd/rimsky-host-agent-proxy/dispatch.go:244-278`, which reads exactly those
  two keys) would never recover an instance on cache miss.
- **Inferred reason:** Bug fix exposed by the change (the plan's GET-fallback
  design implicitly required these fields on the response; they weren't there).
  Per the repo's "fix every bug you find" rule, the implementer fixed it.
  (Implementer-flagged; verified.)

## 16. Scenario tests use the REAL proxy binary via a shared harness, not the in-process `proxy.NewServer` the plan suggested (Pass 10, auditor finding)

- **Plan said:** Task 54 — "Spin up an in-process proxy (call
  `proxy.NewServer(state, cfg)` directly — bypass the binary; bind to a free
  port)." Two scenario files named:
  `host_agent_late_bind_executor_test.go` and `host_agent_failure_modes_test.go`.
- **Implemented:** A third, unnamed shared scaffold
  `test/scenarios/host_agent_harness_test.go` builds the
  `rimsky-host-agent-proxy` binary once and exec()s it as a real gRPC server on
  a free port, runs the host-agent in-process via `hostagent.Run`, and exec()s a
  real `stubchild` test binary over `RIMSKY_AGENT_PORT`. The supervisor resolves
  a late-bound `codegen` executor through the proxy registered as `codegen-proxy`.
  This is the real dispatch path end-to-end, not an in-process fake.
- **Inferred reason:** Substituted approach — higher-fidelity integration than
  the plan's in-process-fake suggestion (`cmd/` `package main` binaries can't be
  imported for an in-process `proxy.NewServer`, which the plan's suggestion would
  have required exporting). The extra harness file is unnamed-but-implied test
  infrastructure. (Auditor finding; the binary-vs-in-process substitution is the
  notable part.)

## 17. Scenario / harness support helpers the plan didn't enumerate (Pass 7/10, auditor finding)

- **Plan said:** Tasks 36–40 wire the late-bind machinery; the scenario tasks
  assume the harness can express it.
- **Implemented:** `graph/scenario/harness.go` grew ~264 lines, including new
  `HarnessOpts` fields the plan didn't name — `LateBindServiceProxies`
  (wraps the executor resolver in a `LateBindResolver`, threads the admit-list,
  wires `LifecyclePeersForSpec` on both supervisor and control-api) and
  `ExecutorProtocols` (overrides a peer's `protocols:` list so the proxy can be
  marked `lifecycle_subscriber`) — plus a `parseUUIDStr` helper and an
  `executorsCfg` builder shared by supervisor + control-api. Extra CLI unit-test
  files also appeared (`control/cli/{run_flags_test,aliases_test,auth_login_test,agent_test}.go`).
- **Inferred reason:** Necessary test/wiring scaffolding to exercise the feature;
  the plan implied tests without naming every fixture file or harness knob.
  Supportive, not behavior-changing for production. (Auditor finding.)

---

## Verified-as-matching (no divergence)

The following plan elements landed essentially verbatim and are called out only
to show they were checked: the `host_agent.proto` shape (modulo #1); the
lifecycle.proto additions (`service_bindings`, `owner_api_key_id`,
`OnRunScopeTerminal`); the Go `LifecycleSubscriber` interface + alias re-exports
+ peer-client adapter; `LateBindResolver` semantics; the `NewRegistry(opts...)`
functional-options shape; the `instanceID` threading through
`acquireOneLock`/`acquireClaim`/`acquireFanOutIfDeclared`/`AcquireSubClaimsInput`;
the per-call interceptor install on the executor + claim-producer dial sites
(with TODO comments left on the other peer-client dials); the supervisor
outbound-lifecycle wiring + `DialLifecycleSubscribers` export; the
`validatorHooksFor` late-bind bypass; the proxy state machine / agent server /
executor + claim-producer handlers / lifecycle consumer / unimplemented handlers
/ http-forward; the host-agent daemon (spawn/exec/dispatch/reap, local-HTTP
listener, `RIMSKY_AGENT_PORT` child contract); the `rimsky agent {start,status,stop}`
subcommand (return-`int` convention) and `rimsky run` additive flags + alias
resolution + `auth login` + `Context.api_key`; and the three new tension files +
11 concept mutations + 2 new concept files + concepts.md TOC entries.

Pre-existing license-classification violations reported by
`cmd/rimsky-license-check` are all outside this plan's 108-file diff and are not
plan work (per the task brief); the plan-introduced files are correctly
classified.
