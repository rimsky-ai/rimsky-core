# Completion report: 2026-08-23-row-bytes-outbox-and-log-kinds

## Stages

1. done — **Attribute bytes in the row.** Byte columns for the bag and scratch; Remove the blob backend; Documentation and gotchas.
2. done — **The outbox and its drain.** Staging moves to foundation; The reconciler runs in every role and gains a kick and a due time.
3. done — **Every lifecycle transition staged.** Run-scope terminal staged at scope close; Instance terminated staged at the transition; The idempotency ledger goes; The proxy's reap through the outbox.
4. done — **Service delivery health.** Peer delivery health.
5. done — **The vocabulary sweep.** "Peer" becomes "service"; "Host agent" becomes "host daemon"; every vocabulary-sweep corpus delta.
6. done — **Log kinds.** Rename every process-log kind; A lint keeps the old form out.
7. done — **Tests on the standard.** Remediate the fifty-five tests; The wall-clock lint reads two more constructs.
8. done — Finish the completion report.
9. pending — Run `/certify-work .ok-planner/sprints/2026-08-23-row-bytes-outbox-and-log-kinds.md`.
10. pending — Walk the presentation.
11. pending — Offer archive-and-commit.

## Work done

### Stage 1 — Attribute bytes in the row

**Byte columns for the bag and scratch.**

- New migrations `lib/foundation/persistence/postgres/migrations/046-attribute-bytes-in-the-row.sql` and
  `lib/foundation/persistence/sqlite/migrations/046-attribute-bytes-in-the-row.sql`: `rimsky_node_attributes.data`
  and `dispatch_input_bag` become `BYTEA` / `BLOB`, `value_handle` and `value_handle_backend` are dropped,
  `rimsky_node_runs.scratch_inline` is renamed to `scratch`, `scratch_handle` and `scratch_handle_backend` are
  dropped, and `rimsky_blob_orphans` is dropped. The SQLite migration rebuilds the attribute table (the
  migration-021 pattern) because two column types change; `rimsky_node_runs` takes a rename and two drops.
- `lib/foundation/persistence/attribute_bytes.go` is the new shared home for the engine cap (`MaxValueBytes`,
  the smaller engine's per-value cap), `CheckValueSize` (the over-cap error naming the node run, the value, and
  the byte count), and `MergeAttributeBag` (the read-merge-write both backends now share, replacing Postgres's
  `jsonb ||`).
- Both drivers' `node_attributes.go` were rewritten: no handle columns, no spill branch, no backend-mismatch
  check, byte scans, and a size check at every write. Both `queue_park.go` files carry the new
  `LoadScratch(ctx, id, tx) ([]byte, error)` / `WriteScratch(ctx, id, scratch, tx) error` shapes.
- `persistence.DispatchRequest` and `NonCascadeStaleInput` collapse their three scratch fields to
  `InitialScratch []byte`; `Queue`, `Tables` (no `BlobOrphans()`), and `Database` (no `SetBlobBackend`) shrink
  with them.
- `TransientParkSignalPayload.scratch_spilled` is retired in `lib/protocols/proto/v1/events.proto`
  (`reserved 4; reserved "scratch_spilled";`) and regenerated.

**Remove the blob backend.** Deleted the backend interface and its four implementations, the spill and orphan
code, the orphan sweep and its scheduler interval, the blob handle on the callback server, on `RunArgs`, and on
the supervisor config, the blob wiring in `lib/control/config` and `lib/control/launch` (the three role
launchers no longer take a pre-opened backend), the `persistence.blob` configuration section whole, the
memory-backend topology gate, the `blob-backend` conformance battery, its command, and its entry in the
conformance verb list, and the storage-conformance cases that exercised spill or orphans. `ProcessRoleEnv`
moved to `lib/foundation/persistence/topology.go`; the marker stays and the roles still read it. Because the
YAML loader decodes strictly, a deployment that still sets `persistence.blob` now fails at startup naming the
key.

**Tests.** New: `lib/foundation/persistence/attribute_bytes_test.go` (the cap admits every size the engine
accepts, and the over-cap error names run, value, and byte count),
`lib/foundation/persistence/conformance/attribute_bytes_in_the_row.go` (a 512 KiB bag, dispatch input bag, and
scratch round-trip whole from the row, run against both drivers),
`lib/runtime/runner_terminal_park_fixture_test.go` (a 1 MiB parked scratch round-trips through real SQLite).
Rewritten: the scratch-load, terminal-scratch, park-signal, scheduler nil-clock, unified-stack, retention-sweep,
compose-artifact, and all-in-one scenario tests, each keeping the proof it alone carried and dropping the spill
instrument. Deleted with the backend: the four redundant blob tests the ledger names, the rest of `blob_test.go`
and `blob_roundtrip_test.go`, `carry_forward_bag_test.go`, both drivers' spill tests, the pg-large-object tests,
the orphan-sweep tests, and the `blobbackend` conformance runner and its tests.

**Documentation and gotchas.** Removed the `CLAUDE.md` gotcha's memory-blob clause (the marker's live purpose —
metrics-listener placement and the SQLite shared-file warning — replaces it), and removed every blob-backend,
spill-threshold, and `blob-backend`-conformance row and section from `docs/` (`capabilities`, `cli`, `concepts`,
`config`, `env-vars`, `grpc`, `images`, `operating`, `protocols/conformance`, `protocols/events`,
`examples/audit-artifact`, `examples/opaque-executor-scratch`, `examples/single-process-all-in-one`,
`cookbook/journey-split-roles-postgres`), plus the conformance Dockerfile's subcommand list.

**Corpus deltas applied.** Retired `concept:blob-backend`; amended `concept:persistence-database` (inline body)
and `concept:conformance`, `concept:inertness`, `concept:node-run`, `concept:rimsky-yml` (sidecar); new
`decision:attribute-bytes-in-the-row`; retired `decision:blob-backend`, `blob-backends-pluggable`,
`blob-spill-threshold-config`, `blob-backend-mismatch-read-refused`, `memory-gate-premise-corrected`,
`process-role-unified-message-covers-rimsky-run`; amended `decision:artifact-layout`, `scratch-column`,
`single-process-mode`, `launch-integration`, `launch-config-injection`, `intx-suffix-convention`; amended
`story:single-process-all-in-one`.

**Checks run.** `go build ./...`, `go vet ./...`, `gofmt -l`, `make lint` (golangci-lint over all four modules
plus license-check), `node .ok-plumbline/bin/plumbline lib cmd test tools`, `go run ./tools/wallclock-lint`
(0 violations, baseline unchanged), `python3 .ok-plumbline/bin/catalog-toc --check`. Tests:
`./lib/foundation/...` (including the persistence conformance suite against real SQLite and real Postgres via
testcontainers), `./test/plumbline/...`, `./lib/runtime/...`, `./lib/control/...`, `./cmd/rimsky/...`, and the
scratch-round-trip and retention-sweep scenarios. All pass. One pre-existing environment failure stands:
`cmd/rimsky/cli` `TestCtxDemo` needs `RIMSKY_IMAGE_TAG` and built images, which this stage did not build.

### Stage 2 — The outbox and its drain

**Staging moves to foundation.** `lib/foundation/lifecycle/staging.go` is the new home for the staging primitive
(`StageDeliveries`), the typed wrappers (`StageTemplateEvent`, `StageInstanceEvent`), the event enum
(`lifecycle.Event`, gaining `EventRunScopeTerminal`), the staged payload (`StagedPayload`, gaining
`run_scope_id` and `terminal_reason`, with `DecodeStagedPayload` beside the marshal), the event-to-protocol
dispatch (`DispatchEvent`), the ledger-state helpers (`TargetStateFor`, `EventDeletesLedgerRow`), and the
subscriber-list resolver (`ServicesReferencedBySpec`, `SubscribingServices`). The new
`StageRunScopeTerminal(ctx, tables, instanceID, runScopeID, terminalReason, lateBindProxies, tx)` stages one row
per subscribing service for a closed run scope, resolving instance → template → services inside the caller's
transaction and failing that transaction when either row is gone. The six control-layer call sites
(`templates.go` x4, `instances.go` x2 through `stageInstanceCreated`) call the foundation functions directly;
`controlapi.LifecyclePeersForSpec` and `peersReferencedBySpec` are gone, and `lib/control/launch` and
`test/support/scenario` build their `peersForSpec` closures from `lifecycle.SubscribingServices`.

**The reconciler runs in every role and gains a kick and a due time.** The staged-delivery drain moved to
`lib/runtime`: `lifecycle_outbox_delivery.go` (the row delivery, the per-scope advisory-lock wrapper
`WithLifecycleScopeTx`, the scope delivery the control-api routes call after commit) and
`lifecycle_reconciler.go` (`runtime.LifecycleReconciler`). Each of the three role launchers in
`lib/control/config` constructs one over the same outbox and runs it, stopping it on shutdown; in the all-in-one
process the three roles run one each over the one database. The drain delivers the oldest pending row per
stream whose `next_attempt_at` has passed; a failure records the attempt, the error, and a next due time on an
exponential backoff from the reconciler interval capped at `service_delivery.stall_after`, and blocks that
stream until then. `Kick()` is a capacity-one channel send that drops when full and wakes `Run` at once; the
control-api kicks its own drain from `deliverStagedLifecycleAfterCommit`, so the interval is retry-only.
Migration 047 in both backends adds `attempt_count`, `next_attempt_at`, and `last_error` to
`rimsky_lifecycle_outbox`, and `LifecycleOutboxTable` gains `RecordAttempt`. The
`service_delivery.stall_after` key (duration, default 5m, positive) loads in its own section of the unified
configuration file and reaches all three role launchers.

**Tests.** New: `lib/runtime/lifecycle_reconciler_test.go` (a blocked stream does not starve another; a failed
delivery records attempt/error/due time and blocks its stream until the due time passes; the backoff widens and
stops at the stall threshold; a staged run-scope terminal is delivered; the kick delivers a row staged after the
last pass without the interval elapsing; a kick on a full channel drops),
`lib/control/config/lifecycle_drain_per_role_test.go` (each of the three roles drains a row nobody else is
running for), `lib/foundation/lifecycle/staging_test.go` (the run-scope wrapper stages one row per subscribing
service with `{run_scope_id, instance_id, terminal_reason}` and refuses a missing instance),
`lib/foundation/lifecycle/subscribing_services_test.go` and `dispatch_event_test.go` (moved with the resolver
and the dispatch), and a persistence conformance case for the row's failure state on both drivers. The
control-layer reconciler tests split: the terminated-instance ones follow the pass to
`terminated_instance_reconciler_test.go`, the drain ones drive `runtime.LifecycleReconciler`.

**Corpus deltas applied.** New `decision:lifecycle-drain-per-role` (inline body), with its line in
`.ok-planner/design/decisions.md`.

**Checks run.** `go build ./...`, `go vet ./...`, `make lint`, `node .ok-plumbline/bin/plumbline lib cmd test
tools`, `go run ./tools/wallclock-lint` (0 violations). Tests: `./lib/foundation/...` (persistence conformance
against real SQLite and real Postgres), `./lib/runtime/...`, `./lib/control/...`, `./lib/graph/...`,
`./test/plumbline/...`, `./cmd/...`. All pass except the pre-existing `cmd/rimsky/cli` `TestCtxDemo`, which
needs `RIMSKY_IMAGE_TAG` and built images.

### Stage 3 — Every lifecycle transition staged

**Run-scope terminal staged at scope close.** `lib/graph/frame/engine.go` stages the run-scope rows inside the
frame-end transaction: `closeSettledFrameScopeTree` now returns only the scopes that transaction closes (deepest
first, children before parents) and `transitionFrameEnd` calls `lifecycle.StageRunScopeTerminal` for each before
the transaction commits, then kicks the drain after it does. `frame.RunScopeTerminalFanout` is replaced by
`frame.LifecycleDelivery{LateBindServiceProxies, Kick}`, which the scheduler builds from its own config. The
supervisor's two child-scope closes in `lib/runtime/child_execution.go` (sub-graph exit, fan-out partition
settle) stage the same rows inside the settlement transaction and kick after commit. `lib/runtime/lifecycle_fanout.go`
(`FrameRunScopeTerminalFanout`, `FanOutRunScopeEvent`) is deleted, and `RunArgs`, `CallbackServer`,
`runtime.Config`, `scheduler.Config`, `SchedulerConfig`, and `SupervisorConfig` lose `LifecycleSubs` and
`LifecyclePeersForSpec` for `LateBindServiceProxies` plus a `LifecycleKick`.

**Instance terminated staged at the transition.** `handleTerminateInstance` closes the instance's open run scopes
and stages a run-scope terminal for each, then stages `instance_terminated`, all inside the transaction that
stamps `terminated_at`; after commit it delivers each staged scope inline. `handleDeleteInstance` purges nothing —
the `LifecycleIdempotency().DeleteByScope` and `LifecycleOutbox().DeleteByScope` calls and
`collectRunScopeIDsForInstance` are gone — closes any scope still open, and delivers inline (F1).
`controlapi.TerminatedInstanceReconciler`, the direct fan-out functions in `lib/control/controlapi/lifecycle.go`
(`FanOutInstanceEvent`, `fanOutRunScopeEventForPeers`, `CloseAndFanOutRunScopesForInstance`,
`fanOutInstanceTerminatedFromLifecycleRows`), and `InstanceTable.ListTerminatedWithLifecycleRows` on both drivers
are deleted.

**The idempotency ledger goes.** Migration 048 in both backends drops `rimsky_lifecycle_idempotencies`.
`persistence.LifecycleIdempotencyTable`, its row and state types, both drivers' implementations, the `Tables`
accessor, `lifecycle.TargetStateFor`, `lifecycle.EventDeletesLedgerRow`, and the ledger reads and writes inside
`DeliverStagedLifecycleRow` are gone. The scope-kind enum is renamed `LifecycleScopeKind` /
`LifecycleScope{Template,Instance,RunScope}` and moves to `lifecycle_outbox.go` (D21). The per-scope advisory
lock stays; its critical section is now a re-read of the row by seq, the service call, and the row delete, and a
new `LifecycleOutboxTable.GetBySeq` makes the re-read possible (D22). The unused
`LifecycleOutboxTable.DeleteByScope` goes with the purge that was its only caller.

**The proxy's reap through the outbox.** The proxy's lifecycle handler is unchanged.
`TestHostAgentReapOnRunScopeTerminal` and `TestHostAgent_MainRunScopeCloseReapsSpawnOnRealInstanceTermination`
already wait on the proxy's own effect (the spawned child's termination log, the child's death), so they now
observe it after the terminate route's staged delivery rather than after a direct call, and they pass unchanged.
The retry path is fired explicitly by `TestTemplateRegister_FailingSubscriberLeavesTemplateRegistered`,
`TestInstanceCreate_FailingSubscriberLeavesInstanceCreated`, and
`TestLifecycleDrain_RedeliveredEventIsNotSentAgainOnALaterTick`, each of which drives `DrainOnce` itself.

**Tests.** New: `TestLifecycleDrain_ABatchOfBackedOffStreamsDoesNotStarveALiveOne` (S2-1),
`TestTerminateInstance_RunScopeTerminalPrecedesInstanceTerminated`,
`TestDeleteInstance_LeavesAnUndeliveredLifecycleRowInTheOutbox`,
`TestStagedDelivery_TwoReplicasSharingOneDBDeliverOneStagedRowOnce`,
`TestCloseAndStageRunScopeTerminals_{DedupesSharedRootScope,PaginatesAcrossFramePages}`, and two persistence
conformance cases (`LifecycleOutboxListsWhatOneServiceIsOwed`, the due-time predicate inside
`LifecycleOutboxCarriesItsDeliveryFailureState`). Rewritten: the lifecycle-scope-lock conformance case now races
over an outbox row instead of a ledger row; `TestFrameSettlement_ClosesRootScopeAndStagesItsTerminalExactlyOnce`
drives the drain; `TestDeleteInstance_SubscriberFailureNeverRefusesTheDelete` replaces the
no-synchronous-fan-out test; the lifecycle e2e scenario asserts the outbox drains empty at every transition.
Deleted with the ledger: `TestTemplateRegistered_ReplayOfADeliveredEventCallsNoSubscriber`,
`TestTemplateDeregistered_DeletesEveryLedgerRow`, `TestFanOutInstanceEvent_TerminatedDeletesRow`, the four
`TestFanOutRunScopeEvent_*` tests, the terminated-instance reconciler tests, and
`LifecycleIdempotencyListByClaimProducer`.

**Corpus deltas applied.** Amended `concept:lifecycle-subscriber`, `concept:supervisor`,
`decision:lifecycle-fanout-after-commit`, and `decision:lifecycle-subscriber-at-least-once-delivery` from the
sidecar, with their lines in `.ok-planner/design/{concepts,decisions}.md`.

**Checks run.** `go build ./...`, `go vet ./...`, `make lint` (golangci-lint over all four modules plus
license-check), `node .ok-plumbline/bin/plumbline lib cmd test tools`, `go run ./tools/wallclock-lint`
(0 violations), `python3 .ok-plumbline/bin/catalog-toc --check`. Tests: `./lib/foundation/...` (persistence
conformance against real SQLite and real Postgres), `./lib/runtime/...`, `./lib/graph/...`, `./lib/control/...`,
and `./test/scenarios/...` (the Postgres-backed scenario suites, including both host-agent reap scenarios and the
lifecycle-subscriber canary). All pass.

### Stage 4 — Service delivery health

**The configuration key.** `service_delivery.stall_after` already loaded in its own `service_delivery:` section of
the unified configuration file (Stage 2); this stage confirmed the section and threaded the value into the
producer-verb dispatcher, which caps its own retry backoff at the threshold (`min(60s, stall_after)`), so a
stalled service is retried no less often than the threshold that declares it stalled.

**Failure state on both rows.** The producer-verb outbox already carried `attempt_count`, `next_attempt_at`, and
`last_error` with a `RecordAttempt` writer, so no migration was needed for it (D28).

**The stall edge.** `lib/runtime/service_delivery_health.go` is the shared detector both drains use:
`ObservePending` marks a service stalled when its oldest pending row has waited longer than the threshold, and
clears the marker of every service whose oldest pending row no longer is, a service owing nothing included
(the shape the Stage 4 fix round settled — see F2). The edge is a row in the new
`rimsky_service_delivery_stalls` table keyed `(service, outbox)` — migration 049 in both backends, with the
`persistence.ServiceDeliveryStallTable` accessor on `Tables`. `MarkStalled` inserts on conflict-do-nothing,
`ClearStalled` deletes, and `ListStalled` names what is marked; the insert's and the delete's report of whether it
changed the row is the edge, so concurrent drains write one entry between them (D29). The lifecycle drain reads its
per-service summary through the new `LifecycleOutboxTable.PendingSummaryByService`; the producer-verb dispatcher
computes the same summary in Go from the rows its pass already loaded, minus the rows it settled.

**The two event kinds.** `OPERATIONAL_KIND_SERVICE_DELIVERY_STALLED` and `..._RECOVERED` join the proto enum with
the wire forms `service_delivery.stalled` and `service_delivery.recovered`, the payload messages
`ServiceDeliveryStalledPayload` (service, outbox, pending count, oldest-pending age in seconds) and
`ServiceDeliveryRecoveredPayload` (service, outbox), and the `events.KindServiceDelivery{Stalled,Recovered}`
constructors. Both reach an `Events().Append`, so `TestEveryOperationalEventKindHasAnEmitSite` covers them.

**The diagnostics route.** `GET /v1/admin/diagnostics/lifecycle-outbox`, gated by `diagnostics:read` beside the
producer-outbox route, lists one object per service with a pending row: the true depth, the oldest staged time and
its age, and per row the scope, the event, the age, the attempt count, the next attempt time, and the last error.
No payload bytes. It is registered in the action catalog and the pinned route count moved from 2 to 3.

**Retention.** `retention.lifecycle_outbox_trailing` already defaults to zero, so at-least-once holds
unconditionally under the shipped defaults as the amended `decision:lifecycle-subscriber-at-least-once-delivery`
states. The decision also says the stall signal makes the failure visible *before* the window discards it, which
nothing enforced: the config loader now refuses a positive window that is not longer than
`service_delivery.stall_after`, naming both keys (D30).

**Tests.** New: `lib/runtime/service_delivery_health_test.go`
(`TestLifecycleDrain_ADeadServiceStallsOnceAndRecoversOnce`,
`TestProducerVerbDispatcher_ADeadProducerStallsOnceAndRecoversOnce`,
`TestProducerVerbDispatcher_TheRetryBackoffNeverOutlastsTheStallThreshold`, joined in the fix round by
`TestLifecycleDrain_AServiceStaysStalledWhileOneStreamBlocksAndAnotherDelivers` and
`TestLifecycleDrain_AServiceStallsAgainAfterTheSweepTakesItsRows`) — each drives the injected clock and
the drain's own pass, and asserts one stall entry and one recovery entry per edge on both outboxes;
`lib/control/controlapi/admin_lifecycle_outbox_test.go` (the route's body, its oldest-first ordering, its absence
of payload bytes, and its refusal of a key without `diagnostics:read`);
`lib/control/config/retention_test.go` (a window shorter than the threshold is refused at load, a wider one
loads); a persistence conformance case `ServiceDeliveryStallMarkerIsAnEdgePerServiceAndOutbox` and two new
assertions inside `LifecycleOutboxListsWhatOneServiceIsOwed` (the limit, and the per-service summary), both run
against real SQLite and real Postgres.

**Corpus deltas applied.** New `decision:service-delivery-stall-signal` (inline body), with its line in
`.ok-planner/design/decisions.md`. The citations D13 and D25 deferred are re-pointed: migration 047 in both
backends, `LifecycleOutboxTable.RecordAttempt` and both drivers' implementations, `outboxRetryBackoff`,
`LifecycleReconciler.recordDeliveryFailure`, `LifecycleReconcilerConfig.StallAfter`,
`DefaultServiceDeliveryStallAfter`, `config.ServiceDeliveryConfig` and its YAML shape,
`SupervisorConfig.ServiceDeliveryStallAfter`, `ListPendingForService` and its two call sites, and the conformance
case for the row's failure state now cite `decision:service-delivery-stall-signal`.

**Documents.** `docs/http-api.md` gained the route and the two event kinds, `docs/permissions.md` and
`docs/operating.md` the route, `docs/concepts.md` the route and the two kinds in its two enumerations, and
`docs/config.md` the `retention.lifecycle_outbox_trailing` row with the ordering constraint the loader now
enforces (D31).

**Checks run.** `go build ./...`, `go vet ./...` (all four modules), `make lint` (golangci-lint over all four
modules plus license-check), `node .ok-plumbline/bin/plumbline lib cmd test tools docs`,
`go run ./tools/wallclock-lint` (0 violations), `python3 .ok-plumbline/bin/catalog-toc --check`. Tests:
`./lib/foundation/...` (persistence conformance against real SQLite and real Postgres), `./lib/runtime/...`,
`./lib/control/...`, `./lib/graph/...`, `./test/plumbline/...`, `./cmd/...`, and `./test/scenarios/...`. All pass
except the pre-existing `cmd/rimsky/cli` `TestCtxDemo`, which needs `RIMSKY_IMAGE_TAG` and built images.

### Stage 5 — The vocabulary sweep

**"Peer" becomes "service".** One sweep over every tracked text file under `cmd/`, `docs/`, `lib/`, `test/`,
`tools/`, `dockerfiles/`, the root build files, and `.ok-planner/surface/`, protecting only the TLS and gRPC
libraries' own names for the far end of a connection (`tls.ConnectionState.PeerCertificates`,
`google.golang.org/grpc/peer`). The configuration key `peer_auth` is `service_auth`; `RIMSKY_PEER_AUTH` is
`RIMSKY_SERVICE_AUTH`; `RIMSKY_PROXY_PEER_GRPC_PORT` is `RIMSKY_PROXY_SERVICE_GRPC_PORT` and the listener it
opens is service-facing; `lib/protocols/peerauth` is `lib/protocols/serviceauth`; `lib/runtime/peer` is
`lib/runtime/service` (D34); `test/permissivepeer` is `test/permissiveservice`; `enroll.PeerAuth{None,MTLS}`,
`enroll.EnvPeerAuth`, `enroll.PeerServerName`, `observability.PeerSpec`, `observability.PeerEntry`,
`ports.HostAgentProxyPeerFacing`, and every other identifier, error message, test name, and prose sentence that
said "peer" for a deployed service now says "service". The permission action `peer-auth:ca-root` is
`service-auth:ca-root`. The fixed TLS server name moved with them (D43).

**"Host agent" becomes "host daemon".** `cmd/rimsky-host-agent` is `cmd/rimsky-host-daemon`;
`cmd/rimsky-host-agent-proxy` — binary, image, `make core-images` target, and `make push-images` entry — is
`cmd/rimsky-host-daemon-proxy`; `lib/runtime/hostagent` is `lib/runtime/hostdaemon`;
`lib/services/internal/agentport` is `lib/services/internal/daemonport` (D39). The CLI verb `rimsky agent
<start|status|stop>` is `rimsky daemon …` and `--agent` on instance creation is `--daemon`. Every
`RIMSKY_AGENT_*` and `RIMSKY_HOST_AGENT_*` variable takes `DAEMON`, and `tools/env-registry` regenerates to
match. `lib/protocols/proto/v1/host_agent.proto` is `host_daemon.proto`, its service is `HostDaemon`, its
messages are `HostDaemonHeartbeat`, `HostDaemonHeartbeatAck`, and `HostDaemonError`, and `Register.agent_label`
/ `.agent_version` are `daemon_label` / `daemon_version`; `make proto-gen` regenerated the bindings and the old
`host_agent*.pb.go` pair is deleted. The wire error classes `host_agent_not_connected` and
`host_agent_disconnected` are `host_daemon_*`, the proxy's daemon-facing listener and its process-log kinds
follow, and the instance-create body field `target_agent` is `target_daemon`. "Agent" stayed wherever it means
an LLM agent (D38). The pin test that keeps "supervisor" out of the proxy's source still passes.

**Corpus deltas applied.** Twenty-two concepts amended from the sidecar (`anonymous-mode`, `api-key`,
`cascade-graph`, `control-api`, `discovery-cache`, `error-policy`, `executor`, `frame`, `instance`,
`module-layout`, `node-subscription`, `observability`, `permission`, `publisher`, `rimsky`, `sensor`,
`service-address-book`, `service`, `template`, `terminal-resolution`, `validation`, plus `supervisor` verified
against the sidecar from Stage 3); `concept:host-agent-proxy`, `concept:host-agent`, and `concept:peer-auth`
retired and `host-daemon-proxy`, `host-daemon`, and `service-auth` created (D36). Twenty decisions amended and
nine retired-and-recreated (`host-agent-*` → `host-daemon-*`, `peer-auth-mtls` → `service-auth-mtls`,
`peer-tls-enforcement` → `service-tls-enforcement`). Two stories amended and nine retired-and-recreated. The
three design TOCs were hand-edited and re-sorted (D37). `node .ok-plumbline/bin/plumbline lib cmd test tools
.ok-planner/design CLAUDE.md README.md docs` is clean, so every `@concept:` / `@story:` / `@decision:` citation
of a renamed slug points at the new file — including the two migration headers that cite
`concept:host-daemon-proxy`.

**Documents.** `CLAUDE.md`, `.ok-planner/surface/surface.md`, the four surface document types, and every file
under `docs/` carry the new vocabulary; the example documents named for renamed stories moved with them
(`docs/examples/service-auth-mtls-mutual.md`, `service-tls-enforced.md`, `permissive-service-build.md`,
`host-daemon-*.md`, `anonymous-daemons-isolated.md`) and every relative link in `docs/` still resolves.
`docs/` port numbers for the proxy and the supervisor callback were corrected (D40); `releases/*.md` were left
alone (D41).

**Checks run.** `go build ./...` and `go vet ./...` over all four modules, `gofmt -w`, `make proto-gen`,
`make lint` (golangci-lint over all four modules plus license-check),
`node .ok-plumbline/bin/plumbline lib cmd test tools .ok-planner/design CLAUDE.md README.md docs` (exit 0),
`go run ./tools/wallclock-lint` (0 violations), `go run ./tools/env-registry` (87 variables, registry
unchanged by the regen). Tests: `./lib/...`, `./cmd/...`, `./tools/...`, `./test/plumbline/...` (the default-port,
env-var-registry, image-EXPOSE, and proxy "supervisor" pin checks), `lib/foundation` and `lib/protocols` whole,
`./test/...`, and the docker-backed `lib/services/test/...` suites against images built under one
`RIMSKY_IMAGE_TAG` by `make core-images service-images test-images`.

### Stage 6 — Log kinds in the standard's form

**Rename every process-log kind.** Every structured process-log emit site under `cmd/`, `lib/`, and `tools/` —
product code and tests alike, the bundled services included — now names its kind as a raw string literal in
`SUBSYSTEM.NOUN.VERB` form. 588 malformed literals across 142 files were rewritten and 43 sites whose message the
scan cannot read statically were resolved: 20 by naming the kind and moving the varying part into a field, 23 by
the pass-through rule (D51). The subsystem vocabulary is D48; the prose rule is D49; the `site` field is D50.
A test that waited on a kind waits on the renamed literal: `lib/services/test/harness/rimsky_split.go` (the split
harness's two container log waits), `lib/services/test/scenarios/subscriber_openlineage_e2e_test.go`,
`test/scenarios/compose_run_one_shot_terminal_test.go`, `cmd/rimsky/cli/compose/{wake_created_instances,shutdown}_test.go`,
`lib/control/controlapi/{admin_waitset,auth_banner,auth_middleware,auth_request_target,enroll,lifecycle,instance_attribute_overrides,instances_static_config_gate}_test.go`,
`lib/runtime/{auth_sweep,publishers_retry}_test.go`, `lib/foundation/locks/registry_test.go`,
`lib/foundation/shared/logger_test.go`, `lib/protocols/serverkit/logging_test.go`, and
`lib/protocols/publisherkit/publisher_test.go`. The event log (`lib/foundation/events/kinds.go`, the proto enum,
`rimsky_events`) is untouched, as are error strings, gRPC status messages, and HTTP response bodies.

**A lint keeps the old form out.** `tools/logkind-lint/` is shaped like the wall-clock lint: `scan.ProcessLogViolations`
walks every `.go` file under `cmd/`, `lib/`, and `tools/` with `go/ast`, finds each call to a slog method
(`Debug`/`Info`/`Warn`/`Error`, the four `*Context` variants, `Log`, `LogAttrs`) on a logger expression, reads the
message argument, and reports `malformed-log-kind` for a literal outside the form and `unreadable-log-kind` for a
message it cannot read. A logger expression is the `log/slog` import, a name the tree ever declares with type
`*slog.Logger`, `shared.Logger`, or `*CapturingLogger` (fields, parameters, vars, and `:=` bindings reached by
fixpoint, plus every function whose single result is one of those types), or a `With` / `WithGroup` / `Default` /
`New` call on one. `main.go` carries the wall-clock lint's CLI — default writes `baseline.json`, `-list` prints —
plus `-check`, which writes nothing and exits 2 on any violation (D53). The baseline is empty.
`test/plumbline/logkind_ratchet_test.go` is the suite's gate (`TestStructuredLogKindFormat` holds the empty
baseline; `TestEveryProcessLogKindIsReadableAtItsEmitSite` fails every unreadable message), and `make lint` now
depends on `logkind-lint` beside `license-lint`. `tools/logkind-lint/scan/scan_test.go` holds the scan's own rules:
the form accepted and each way it is broken, an unreadable message, a forwarded kind, a non-logger receiver, every
logger spelling, and which violation kind the baseline may record.

**Corpus deltas applied.** New `decision:structured-log-kind-format` (inline body), with its line in
`.ok-planner/design/decisions.md`. The lint, its CLI, its fitness test, and the Makefile target cite it.

**Documents.** Four `docs/` sentences said the entrypoint reports `migrate failed` and one example named the
`bundled executor registered in-process` line; both now name the kinds the code emits (D56).

**Checks run.** `go build ./...` and `go vet ./...` over all four modules, `gofmt -l` (clean), `make lint`
(license-check, logkind-lint `-check`, golangci-lint over all four modules),
`node .ok-plumbline/bin/plumbline lib cmd test tools docs .ok-planner/design Makefile CLAUDE.md README.md`,
`go run ./tools/logkind-lint` (0 violations, empty baseline), `go run ./tools/wallclock-lint` (0 violations).
Tests: `./test/plumbline/...`, `./tools/...`, `./lib/control/...`, `./lib/graph/...`, `./cmd/...`,
`./lib/runtime/...`, `lib/foundation` whole, `lib/protocols` whole, the `lib/services` module's non-harness
packages, and `./test/scenarios/... -run TestComposeRunOneShot`. All pass except two pre-existing environment
failures that need `RIMSKY_IMAGE_TAG` and built images: `cmd/rimsky/cli` `TestCtxDemo` and the two
`lib/services/subscribers/openlineage` harness tests.


### Stage 7 — Tests on the standard

**Remediate the fifty-five tests.** The audit ledger at `.ok-planner/workbench/2026-08-22-test-audit-ledger.tsv`
names fifty-five rows off the passing verdict: 29 redundant, 16 wall-clock, 10 vacuous. Four of the redundant
rows were the blob tests Stage 1 deleted with the backend
(`lib/foundation/persistence/blob_test.go::TestFilesystemBackend`,
`TestFilesystemBackendReadRangeOutOfBounds`, `TestMemoryBackend`, `TestMemoryBackendReadRangeOutOfBounds`), and one
more — `lifecycle_fanout_after_commit_test.go::TestLifecycleReconciler_RunScopeTerminalPrecedesInstanceTerminated`
— Stage 3 dissolved when it deleted the sibling the ledger named as the keeper (D58). The remaining fifty are
remediated here; none survives in the form the ledger recorded.

- **Deleted as redundant, with the assertion each alone carried folded into the keeper.**
  `cascade/state_test.go` loses `TestFreshAndFailedAreTerminal`,
  `TestRunningToRunningUnderDispatchClaimedIsRejected`, `TestNextState_AggregateSettlementIsIllegalForLeafRuns`, and
  `TestNextState_ReleaseReturnsAClaimedRunToStale`; the exhaustive matrix in `TestTransitionTable` gains the
  zero-value assertion (`NextState` returns no state on an illegal transition) the second of them alone carried.
  `locks/conflict_test.go::TestModeCoexistsSymmetric`, the four
  `conformance/executor/callback_receiver_test.go` duplicates (`RejectsAllThreeOutcomes`, `RejectsReservedEventsField`,
  `LateCallback_AfterConsumptionRejectedNotDelivered`, `HandleSuccessReturns204`) — the reserved-`events` regression
  becomes a named case inside `TestParseCallbackBody_RejectsUnknownTopLevelField` —
  `filesystem/server/server_test.go::TestSplitScope_ListTraversalAndDuplicateKeysCollapseToDistinctScopes`,
  `filesystem/store/patterns_test.go::TestPattern_StagePromote` and `TestPattern_QueueMode_AutoRefresh`,
  `filesystem/store/validator_test.go::TestValidator_RejectsNullCommit` (its message assertion folded into
  `TestValidator_RejectsMissingFields`),
  `sensor-cron/deterministic_idempotency_key_test.go::TestOneFireWindowPostsOneEnvelopeNamingItsSubscription`,
  `sensor-webhook/state_db_test.go::TestSubscribe_ResyncReloadsWatermarkAfterRestart`,
  `controlapi/app_test.go::TestClaimHoldersRoute_EmptyList`, the whole file
  `controlapi/instances_pause_idempotency_test.go` (its two body assertions folded into
  `instance_kill_teardown_test.go::TestPauseResume_NonIdempotentVerbs409`),
  `controlapi/messages_test.go::TestCreateMessage_DeclaredTypeAccepted`,
  `runtime/runner_send_message_test.go::TestSendCascadeMessageInTx_ReplayDoesNotDoubleInsertEnvelope`,
  `runtime/subgraph_dispatch_test.go::TestSubgraphParentSuccessCascade_ReturnsInternals`,
  `test/scenarios/acquire_unavailable_pass_test.go` (its Open-observed assertion folded into
  `TestAcquirePassSubscribedMonitorRuns`), `test/scenarios/auth/grant_scope_test.go` (its tag-applied and
  denial-reason assertions folded into `grant_scope_lifecycle_test.go::TestGrantScope_TemplateRegister_HashForm`,
  which also takes over `templateHasTag`), `test/scenarios/auth/lifecycle_test.go::TestRotation_DualActiveAndSweep`,
  and `test/scenarios/run_tree/fanout_aggregation_test.go::TestFanoutAggregation_PolicyTable`.
- **Vacuous tests given an assertion on the behavior their name claims, or deleted.** The two
  `TestRegistryConcurrentAddAndReadIsRaceFree` tests (`foundation/lifecycle`, `foundation/locks`) become
  `TestRegistryConcurrentAddsAllLandAndEveryNameResolves`: every concurrent `Add` lands, `Get` resolves each name,
  the map holds the full count, and every concurrent `Names()` snapshot names only entries `Get` resolves.
  `sqlite/database_test.go` drops the constant-inequality check for
  `TestOpenWriteTransactionDoesNotStarveAConcurrentReader`, which opens a real database, holds a write transaction
  open, and proves a reader on a second pool connection still runs.
  `sensor-http/sensor_test.go::TestTick_PollsDueWatchesConcurrently_OneSlowWatchDoesNotBlockAnother` now proves the
  interleaving (the fast upstream is served while the slow handler is parked, the tick has not returned, and both
  watches record a poll) instead of proving only that nothing deadlocked.
  `controlapi/mcp_resources_test.go::TestResources_Read_LimitCappedAtMax` asserts `parseSinceLimit` clamps 9999 to
  the ceiling, honors a limit under it, and defaults an absent one.
  `rimsky-host-daemon-proxy/claim_producer_handler_test.go::TestClaimProducerObsCapabilities` becomes
  `…AdvertisesNoObservabilityOfItsOwn` and asserts every field of the envelope. Deleted outright:
  `rimsky-host-daemon-proxy/lifecycle_handler_test.go::TestNoOpLifecycleMethods`,
  `control/launch/unified_test.go::TestUnifiedStack_DrainEmptyIsNoOp`,
  `runtime/hostdaemon/local_http_test.go::TestBootstrapTokenLifetimeOutlastsLeafRenewalInterval` (a tautology over
  two constants), and `test/scenarios/claim_producers/open_rollback_test.go` (a `t.Skip` pointing elsewhere).
- **Wall-clock waits rewritten onto the event-driven form.**
  `sqlite/advisory_locker_test.go::TestAcquireMigrationLock_HonorsContextCancel` now blocks a second locker,
  waits on `contendedPolls()` the way its sibling does, then cancels and asserts `context.Canceled`.
  `conformance/executor/await_terminal_test.go::TestAwaitTerminal_ContextCancelledWhileAwaiting` cancels explicitly
  instead of waiting out a 100 ms deadline. `conformance/claimproducer/serialization9b_test.go` drives the check's
  reader-open bound itself: `checkSerialization9b` takes the bound as a parameter (production passes the 2 s
  timeout), the fake signals when both readers park, and the test ends the bound on that signal — the suite no
  longer spends two real seconds there (D59).
  `conformance/executor/runner_test.go::TestRun_AllowLiveSkipsStubRequiringScenarios` stops appending to the
  package-level `registered` slice: `RunnerOpts` gains a `Scenarios` field, defaulting to `All()` (D60).
  `runtime/producer_verb_outbox_test.go::TestProducerVerbDispatcher_RunDeliversOnKickWithoutClockAdvance` waits on
  the store's observed commit through `awaited.Until` instead of busy-polling the outbox with `runtime.Gosched`.
  `test/support/executors/stub/stub_test.go::TestDelayRespectsContextCancellation` gives the stub an hour-long
  delay and cancels once the stub has recorded the dispatch, so the margin between two real durations is gone.
  `test/fixtures/demos/host-daemon-control-plane-demo.sh` — the script
  `test/scenarios/host_daemon_control_plane_demo_test.go` runs — replaces its two `date +%s` deadline polls:
  `wait_dialable` becomes `poll_until_dialable_or_process_exits`, which polls without a deadline and fails only
  when the watched process exits first, and the post-stop pid loop waits on the exit alone (D61).
- **Product waits that took the Clock.** The claude-agent silence and tool-use timeout loop now takes `Now` and a
  timeout ticker from `AgentRunOptions`; the four timeout tests drive both and no longer wait out 100–400 ms of
  real time (D62). The daemon-stop escalation takes a `daemonProcessControl` — process probe, signals, clock, and
  the two windows — so `TestDaemonStopEscalatesToSigkillWhenProcessIgnoresSigterm` proves the SIGTERM-then-SIGKILL
  order and that the SIGKILL waited the full grace window on the daemon's own clock, with no real process and no
  real time (D63). `WaitForControlAPIReady` takes a `shared.Clock` and drops its `context.WithTimeout`/ticker pair;
  `TestWaitForControlAPIReady_DeadlineExceeded` drives it with the new `shared.AutoAdvanceClock` and now also
  asserts the wait kept polling (D64). The compose drain's child grace window becomes an injectable
  `ChildGrace` on `ShutdownCoordinator`; `TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime` becomes
  `TestDrain_SIGKILLsAChildThatOutlivesTheGraceWindow` and fires the grace expiry itself, dropping five real
  seconds from the suite. `http-node`'s `Opts` gains `Now`, so the 429 park test asserts an exact `resume_at`
  instead of a ±2 s window.

**The wall-clock lint reads two more constructs.**

- **A context deadline whose expiry feeds a verdict.** A `context-deadline` detector matches
  `context.WithTimeout(` / `context.WithDeadline(` and fires only where the enclosing function also names
  `DeadlineExceeded` — the mechanical reading of "whose expiry feeds a verdict" (D65). Unmarked it is an
  unclassified wait; a class marker with a justification admits it, for the deadline that is itself the input
  under test.
- **A test that writes a package-level variable.** `tools/wallclock-lint/scan/packagestate.go` parses each
  directory's package with `go/ast`, collects the package-level variable names across all its files (test and
  product alike), and reports an assignment or increment to one from a test function — a `Test`/`Benchmark`/`Fuzz`/
  `Example` function, or a helper taking a `testing.TB`-family parameter (D66). Locals that shadow the name, and
  writes from product code, are not read. The kind `test-writes-package-state` is not baselineable, so the gate
  fails on it at once; a class marker over such a write is `inadmissible-under-any-class`.
- **Lint unit tests.** `scan_test.go` gains seven: an unmarked verdict-feeding context deadline fails and names the
  construct; a deadline that only bounds an operation is not read; a deadline under test is admitted with a class
  marker; a test writing a package variable fails, naming variable, test, and line; a `testing.TB` helper's write
  fails while a shadowing local does not; no class rescues a write to package state; and a write from product code
  is not a test violation.
- **The sites the new constructs surfaced beyond the fifty-five, all fixed so the baseline stays empty.**
  `cmd/rimsky-entrypoint`'s `binaryDir` becomes a parameter on `spawn` / `spawnRole` / `startOnce` /
  `runMigrateIfOwned` / `runSingleRole`, with `defaultBinaryDir` at the top-level call sites (12 writes gone).
  `cmd/rimsky`'s `resolvedVersion` splits into `versionOrBuildInfo(stamped string)`, which the test calls directly
  (4 writes gone). `lib/control/config`'s `resyncPublishersAtStartup` and `runPublisherSubscriptionReconciler`
  package seams become `ResyncPublishers` and `ReconcilePublisherSubscriptions` fields on `ControlAPIConfig`,
  defaulting to the runtime functions (4 writes gone). `lib/control/launch`'s `runSchedulerFn` / `runSupervisorFn` /
  `runControlAPIFn` become a `roleRunners` value that `startUnifiedStack` takes explicitly, with
  `StartUnifiedStack` delegating through `defaultRoleRunners()` (24 writes gone). `cmd/rimsky/cli/compose`'s
  `startRoleStackFn` becomes the last parameter of an internal `startRoleStack` (2 writes gone).
  `cmd/rimsky/cli`'s `daemonSelfExecutable` becomes a parameter of `daemonize`, and the two stop windows become
  constants on `daemonProcessControl` (6 writes gone). The claude-agent module registry stops being written from
  tests: `AgentRunOptions.McpModules` and `Opts.McpModules` carry an explicit module map, `ParseCliConfig` takes
  it, and `standUpModuleLoopback` resolves the injected map before the package registry (D67).
  `lib/services/test/harness/executor_stub.go`'s two memo maps move onto a `stubRegistry` type whose `ensureOn`
  method owns the lock and the write (D68).

**Corpus delta applied.** `decision:test-wallclock-lint-ratchet` amended verbatim from the sprint; its
`decisions.md` summary now names the two new constructs.

**Checks run.** `go build ./...` and `go vet ./...` across all four modules, `gofmt -l`, `golangci-lint run` in
each of the four modules, `make license-lint`, `node .ok-plumbline/bin/plumbline lib cmd test tools` (0
violations), `go run ./tools/wallclock-lint` (0 violations, baseline `{}`), `python3
.ok-plumbline/bin/catalog-toc --check`. Tests: `./tools/...`, `./test/plumbline/ -run Wallclock|EveryDeclaredWaitClass`,
`./lib/foundation/{cascade,locks/...,lifecycle,shared,persistence/sqlite}`, `./lib/protocols/...` (whole module),
`./lib/control/...`, `./lib/runtime/...`, `./cmd/...`, `./lib/services/{claim_producers/filesystem/...,executors/claude-agent,executors/http-node,sensors/...}`,
`./test/support/...`, `./test/scenarios/{,auth,run_tree,claim_producers}` — all pass. Two known reds are not this
stage's: `make lint`'s `logkind-lint` step and `test/plumbline`'s two log-kind ratchet tests fail on `t.Error(...)`
call sites now that Stage 6's in-flight fix round added `test` to that lint's scan roots (Builder 6 owns
`tools/logkind-lint/**`), and `cmd/rimsky/cli::TestCtxDemo` needs `RIMSKY_IMAGE_TAG` and built images.

## Fix rounds
### Fix-only round 1 — reviewer findings S7-1 … S7-4

- **S7-4 — the cause was a tree-wide identifier-name set, not Stage 7 and not the `test` scan root.**
  `scan.loggerNames` collected *names*: it seeded the set with every identifier the whole tree ever declared with a
  logger type, then grew it by fixpoint over every `x := <logger expression>` assignment in every file. Because
  `isLoggerExpr` resolved a selector by `names[sel.Sel.Name]` and an index expression by its base, an unrelated
  local joined the set at every hop. This chain put the bare `t` in it:
  `idx` (`test/support/executors/stub/stub.go:305`, `idx := n`) → `i` (`cmd/rimsky/cli/compose/wait_test.go:61`,
  `i := f.idx[id]`) → `start` (`tools/wallclock-lint/scan/scan.go:62`, `start = i`) → `now`
  (`lib/control/controlapi/auth_middleware.go:245`, `now := start`) → `t`
  (`lib/services/sensors/sensor-cron/sensor.go:297`, `t := now`). Every `t.Error("prose")` in the tree then read as
  a logger emit site: 64 violations, none of them a log kind. Adding `test` to the scan roots only made those call
  sites visible; the binding came from product code, and the same set explains the reviewer's observation that the
  sensors' `s.logger.Warn(...)` sites were read only because some other package declares a `logger` field.
  The scan now resolves a receiver by its declaration's type in scope. `tools/logkind-lint/scan/loggertype.go`
  indexes those declarations. Per package directory it records type declarations, receiver-less functions, methods
  by receiver type, and package-level variables. Per file it records every binding — receiver, parameter, named
  result, `:=`, `var`/`const`, range value — with the extent of the block that declares it and either its written
  type or its initializer. `typeOf` resolves an expression to a syntactic type — an identifier
  to its innermost binding, a selector to the named type's field (embedded fields included), a call to its single
  result, an index to the element type — carrying the file the type was written in so imports resolve. A **logger
  type** is no longer a name on a list. It is a type that declares a **logger-shaped method**: one named in the
  emit-site set (`Debug`/`Info`/`Warn`/`Error`, the `*Context` variants, `Log`, `LogAttrs`) whose parameter at the
  message index is a `string` and whose last parameter is variadic. A type that embeds such a type is a logger too,
  and so is `*slog.Logger` from the standard library. That rule reads the tree's narrow logger interfaces:
  `frame.Logger` declares `Info` and `Warn` only, and `publisherkit.Logger` declares `Warn` alone. It refuses
  `*testing.T`, whose `Error(args ...any)` carries no leading message string, and `error`, whose `Error()` takes no
  parameters. Measured against the tree, the scan reads 638 emit sites. That is the set the old scan read, minus
  exactly the 64 `t.Error(...)` sites, plus nothing. Four scan tests
  hold the rule — a `*testing.T` receiver named `t` is no logger, as a parameter or as a struct field; a field
  named `logger` whose type declares `Error(args ...any)` is no logger; a field of a logger type under any name is
  an emit site, both for `*slog.Logger` and for a one-method interface; and a `logger` field in one package does
  not make a `logger` field in another package a logger. `make lint` and both `test/plumbline` log-kind ratchet
  tests are green with an empty baseline.
- **S7-1 — the context-deadline detector reads every deadline in test code (D70, superseding D65).** The
  `NeedsExpiryClaim` gate and `enclosingFunctionClaimsDeadlineExpiry` are gone; `context.WithTimeout` /
  `context.WithDeadline` in test code is now read like every other construct, and a class marker admits the
  legitimate ones. That surfaced 71 sites, settled as follows. **Twenty-eight converted**, because expiry fed a
  verdict: the ten 120-second bounds around `lib/graph/frame/engine_test.go`'s Postgres-backed bodies, the six
  ten-second bounds around the two `lifecycle_gate_test.go` gRPC calls, the two 90-second container-boot bounds in
  the postgres claim-producer boot helpers, `serviceauth_test.go`'s one-second bound on a fail-closed `Load`, the
  60-second `exec.CommandContext` bound in the claim-producer conformance CLI runner, and the five dial/run bounds
  in the two conformance scenarios — each now takes the ambient context, so a hang is the run guard's business and
  no elapsed time decides a verdict. **Three rewritten onto event-driven forms**, the three the ledger named:
  `lib/runtime/fanout_dispatch_test.go:127` no longer waits out a 30 ms deadline on a full semaphore —
  `FanOutParallelismSemaphore` now counts its parked acquirers (`Waiting()`, incremented only on the blocking path)
  and the test parks a third acquire, blocks on the count reaching one, then proves the release hands it the slot;
  `lib/services/executors/http-node/{server_test.go:160,sole_deadline_test.go:68}` take a context whose deadline the
  test fires on an event — `firedDeadlineContext` (new, `fired_deadline_test.go`) closes its `Done()` channel on
  demand and reports `context.DeadlineExceeded`, and each test fires it when the upstream handler is entered, so
  the executor still classifies `http/timeout` with no real time spent (both tests now run in 0.00 s, against 50 ms
  and 150 ms). **Forty-three marked**: forty-two `pacing` and one `outcome`. The `pacing` forty-two are teardown
  and shutdown graces — `container.Terminate(termCtx)` in the services harness and the container-backed tests, the
  role and bridge `Shutdown(ctx)` calls in `test/support/scenario/harness.go` and the two publisher tests, the log
  tail read from a failed container, the two stub binaries' own bounds under `testdata`, and the claude-agent
  graceful-shutdown grace that is itself the input under test — each carrying a justification saying the error is
  discarded and no verdict reads it. The one `outcome` is `agentport_e2e_test.go:75`, whose per-attempt dial bound
  sits inside a poll that returns only on success. The baseline stays `{}`.
- **S7-2 — the package-state check reaches a write through a call (D71, superseding D66; fork F4).**
  `packageStateWriters` builds, by fixpoint over the package, the set of receiver-less functions that write a
  package-level variable **from a value handed in**: a function qualifies when an assignment to a package variable
  takes a parameter (or a selector, index, address, or dereference rooted at one) as the assigned value or as the
  index it writes under, or when it passes one of its own parameters to a function already in the set. A test's
  call to such a function is the test's write, reported at the call site and naming the variable, the callee, and
  the test. `test/scenarios/auth/grant_scope_lifecycle_test.go`'s `randomNoun` → `nonce` counter, the services
  harness's memoised docker network, and the CLI verbs that install their own output flags stay unread: those write
  a value the product computes for itself, and the test hands nothing in (F4). The widened check surfaced 39 sites
  across two seams, both swept. `lib/runtime/service` gains an explicit `ClientCredentials{RootCAs, Identity}`
  value: `TLSClientConfigFor`, `TransportCredentialsFor`, `DialWith`, and `DialPublisherWith` take one,
  `TLSClientConfig` / `TransportCredentials` / `Dial` / `DialPublisher` pass `ProcessClientCredentials()` so no
  product call site changes, and the thirty-six writes in `dial_tls_test.go`, `mutual_tls_test.go`, and
  `publisher_client_test.go` become one `creds` value per test. `SetTLSRootCAsForTesting` is deleted with them —
  a wrapper whose only job was to launder the write; `lib/runtime/executor/client_http_test.go` calls
  `service.SetTLSRootCAs` directly and `test/scenarios/service_tls_test.go` dials through `DialWith` with explicit
  credentials. In the CLI, `reportDryRunPreviewAs(format, err)` carries the format explicitly and
  `ReportDryRunPreview` passes the active flag, so the dry-run preview test asserts both renderings without
  installing global flags, keeping one assertion that `reportError` routes a preview to exit zero. Three scan tests
  hold the rule: a test's call to a setter is the test's write, a two-wrapper chain launders nothing, and product
  state the test hands nothing into is not read.
- **S7-3 — the sensor exposes its polls in flight.** `SensorService` counts them (`pollsInFlight`, incremented for
  the length of `pollOne`) and `PollsInFlight()` reports the count.
  `TestTick_PollsDueWatchesConcurrently_OneSlowWatchDoesNotBlockAnother` now holds *both* upstreams on one release
  channel, blocks on the count reaching two, checks the tick has not returned while both are held, releases, and
  asserts each upstream served exactly once and each watch recorded a poll. The verdict no longer depends on which
  watch the tick reaches first: a serializing tick never reaches two in flight, so it never passes, in either
  order.

**Checks run.** `go build ./...` and `go vet ./...` in all four modules, `gofmt -l` (clean), `make lint` (green:
license-check, `logkind-lint -check`, golangci-lint over all four modules), `node .ok-plumbline/bin/plumbline lib
cmd test tools` (0 violations), `go run ./tools/wallclock-lint` and `go run ./tools/logkind-lint` (0 violations
each, both baselines `{}`). Tests: `./test/plumbline/...`, `./tools/...`, `./lib/...` and `./cmd/...` in the root
module, `./test/...` (the Postgres-backed scenario suites), the whole `lib/foundation` and `lib/protocols`
modules, and in `lib/services` the `claim_producers`, `executors`, and `sensors` trees plus
`test/scenarios/{claim_producers,atomic_staging}` and `TestSubscriberOpenlineage`. All pass. The image-backed runs
used one minted `RIMSKY_IMAGE_TAG` with `make core-images service-images test-images` built under it, so
`cmd/rimsky/cli::TestCtxDemo` — red in every earlier stage for want of images — passes too.

### Stage 6 — reviewer findings S5-7, S6-1 … S6-2

- S6-1 — the gate now follows a kind past a forwarding shim, and the pass-through exemption binds to a
  declaration. `scan.parameterDeclarations` records every declaring identifier in a file — parameters, receivers,
  named results, short variable declarations, `var` and `const` specs, and range keys — each with the extent of
  the block that declares it. A use resolves to its innermost binding, so a local that shadows a parameter is no
  pass-through. `scan.forwardingKindParameters` records every receiver-less function that forwards a parameter
  into a kind position, together with that parameter's index, and reaches a chain of such functions by fixpoint.
  `forwardedKindViolations` then applies the same rule to the argument at that index in every call to one. Five
  new scan tests hold the rule: a well-formed literal through a helper passes, a malformed one is
  `malformed-log-kind` at the call site, an unreadable one is `unreadable-log-kind`, a two-helper chain is checked
  at the outermost call site, and a local shadowing a parameter name is not exempt. Mutating
  `logFanOutFailures`'s and `retryRPCWithBackoff`'s call sites back to their old literals fails the gate.
  Restoring them clears it. The resolver reads declarations rather than `ast.Object`, which Go 1.22 deprecated and
  staticcheck refuses. D51 now says what stands.
- S6-2 — `scanRoots` gained `test`, and the three literals it exposed now read `STUBSTORE.GRPC.SERVEFAILED` and
  `STUBSTORE.HTTP.SERVEFAILED` in `test/support/claim_producers/stub/server/server.go`, and
  `COMPOSESTUB.GRPC.LISTENING` in `test/support/composestub/main.go`. No test waits on the old literals. D57
  records the change.
- S5-7 — `.ok-planner/design/decisions.md`'s `claude-agent-cli-expose-env-field` summary says "Claude-agent node
  config" again. The Stage 5 sweep had rewritten it to "Claude-daemon", which the sweep's own exemption for the
  claude-agent executor forbids (D38). The tree holds no other `Claude-daemon` string.

### Stage 5 fix round — reviewer findings S5-5 … S5-6

- S5-5 — the repair D47 deferred is done. Nothing reads `RIMSKY_SUPERVISOR_CONFIG` and no second configuration
  file exists, so every site that told an operator to write one now points at `rimsky.yml`'s `supervisor:`
  section. `docs/config.md` drops the supervisor-config row from its file table, reads "three kinds of
  configuration file", gains a `### supervisor` subsection under `## rimsky.yml` carrying the eight keys under
  their `supervisor.` paths, loses the top-level `## Supervisor config` section, and counts fourteen top-level
  keys where it counted twelve. `docs/operating.md` reads one YAML file and enumerates all fourteen keys.
  `docs/images.md` mounts one file and names the `supervisor:` section in it, and the all-in-one paragraph says
  what the image bakes. `docs/concepts.md` drops the variable and the image path from its `rimsky-yml` and
  `supervisor` entries and from its image-path enumeration, which is now three. `docs/cookbook/journey-split-roles-postgres.md`
  folds its second YAML block into the first as a `supervisor:` section, on port 8081 rather than 9100, and mounts
  one file. D47 is rewritten to record the repair.
- S5-6 — `docs/concepts.md`'s environment-variable enumeration is regenerated from
  `tools/env-registry/registry.md`: 87 entries in the registry's order. It gains `RIMSKY_HOST_DAEMON_INSECURE`,
  `RIMSKY_EXECUTOR_VERIFIER_HTTP_EGRESS_ALLOWLIST`, and `RIMSKY_PROXY_LOCAL_CA_FILE`, and loses `RIMSKY_DAEMON_TLS`
  and `RIMSKY_SUPERVISOR_CONFIG`, which no code reads. The heading count moved from 86 to 87.


### Stage 5 — reviewer findings S5-1 … S5-4

- S5-1 — D35 is rewritten as the claimed fork `F3`. It states the two options the reviewer named — a new
  aggregate `GET /v1/observability/services`, or `pending_lifecycle_deliveries` on
  `GET /v1/observability/executors/{name}` as well, since `<service>.protocols` admits `lifecycle_subscriber`
  on an executor entry — alongside the reading Stage 4 built and why. Both options are additive to what stands.
  The architect settles it.
- S5-2 — both sentences now state what `test/plumbline/default_ports_test.go` proves.
  `docs/env-vars.md`'s host-daemon-proxy paragraph says neither proxy port collides, names the two blocks
  (8080-8099 core, 9000-9199 bundled), and names the check. `.ok-planner/surface/documents/operating.md`'s
  `## Covers` line asks for "the public ports, and the two blocks the shipped defaults come from" in place of
  "the pairs that collide by default".
- S5-3 — the `RIMSKY_DAEMON_TLS` row is deleted; no code reads it. `RIMSKY_DAEMON_TLS_CA` is the optional pin:
  set, the daemon verifies the proxy against that root under the fixed service server name; unset, it verifies
  against the system roots, and the dial runs TLS either way. `RIMSKY_HOST_DAEMON_INSECURE` is documented in
  its place as the plaintext switch both ends must set, matching `CLAUDE.md`.
- S5-4 — D44 names the `target_agent` → `target_daemon` body field and the `--agent` → `--daemon` flag. D45
  names the gRPC full-method rename to `/rimsky.v1.HostDaemon/Connect` as the sprint's largest outward break,
  what fails and how, and why pre-v1 licenses it.
- The reviewer's `docs/env-vars.md` pass found three more rows no code backs and one false claim about the
  egress guard. D46 records what changed. D47 records the one repair left whole rather than half-done.
- The prose review over this round's writing caught two error-class globs the Stage 5 sweep had renamed.
  `docs/concepts.md` listed the claude-agent executor's classes as `daemon/*` and `docs/llms.txt` called them
  "the thirteen daemon/ classes". The sweep protected each concrete `agent/<class>` literal but not the glob,
  so both say `agent/*` again. No other `daemon/` string in the tree names an error class.

### Stage 4 — reviewer findings S4-4 … S4-5

- S4-4 — `ProducerVerbDispatcher.DispatchOnce` no longer returns early on an empty outbox, so the health pass
  runs over an empty summary and clears a marker the producer's last rows left behind. New test
  `TestProducerVerbDispatcher_AProducerStallsAgainAfterItsRowsLeaveTheOutbox`: stall the producer, delete its
  row, show the next pass writes the recovery, enqueue again, stall again — three entries. It fails when the
  early return goes back in (checked by mutation).
- S4-5 — D33 now names the two kinds the fix round left standing: `LIFECYCLE.PENDINGSUMMARY.UNREAD` and
  `SERVICEDELIVERY.HEALTH.UNRECORDED`. The separate stall and recovery kinds went with `ObserveDelivered`.

### Stage 4 — reviewer findings S4-1 … S4-3

- S4-1 — both edges now come from `PendingSummaryByService` alone. `ServiceDeliveryHealth.ObservePending` reads
  the services currently marked stalled through the new `ServiceDeliveryStallTable.ListStalled`. It marks every
  service whose oldest pending row is past the threshold. It clears every marked service that is not in that set.
  One transaction holds all three steps, and `MarkStalled`'s insert and `ClearStalled`'s delete still arbitrate the
  edge across concurrent drains. Deleting `ObserveDelivered` also removes the per-delivery transaction the
  reviewer's last observation names. The producer-verb dispatcher keeps subtracting the rows it settled from its
  pass-start snapshot, so its summary names what is still pending. New test
  `TestLifecycleDrain_AServiceStaysStalledWhileOneStreamBlocksAndAnotherDelivers` drives one service with a blocked
  stream and a flowing one through four passes of deliveries, and asserts one stalled entry and no recovery. It
  fails when a clear-on-delivery goes back in (checked by mutation). Recorded as fork F2.
- S4-2 — the clear branch above covers a service absent from the summary, which is what the sweep leaves behind.
  New test `TestLifecycleDrain_AServiceStallsAgainAfterTheSweepTakesItsRows`: stall, sweep the rows away with
  `SweepLifecycleOutbox`, observe the recovery, stage again, stall again — three entries. It fails when the clear
  branch is disabled (checked by mutation). The conformance case also asserts `ListStalled` per outbox.
- S4-3 — `pendingDeliveryPageSize` is deleted. `lib/control/observability/handler.go` passes
  `persistence.DefaultServiceOutboxPageSize`.

### Stage 3 — reviewer findings S3-1 … S3-3

- S3-1 — two tests now hold the guard. `TestFrameSettlement_StagesNoTerminalForAChildScopeAlreadyClosedAtRendezvous`
  seeds a child run scope under the settling frame's root, closes it the way the supervisor closes one at
  rendezvous, drives the frame tick and the drain, and asserts exactly one `on_run_scope_terminal` for the root and
  none for the closed child. `TestTerminateInstance_StagesNoTerminalForAChildScopeAlreadyClosedAtRendezvous` is the
  same shape through the terminate route. Both fail when the `if scope.ClosedAt != nil { continue }` skip is deleted
  from `lib/graph/frame/engine.go` and `lib/control/controlapi/lifecycle.go` respectively (checked by mutation).
- S3-2 — `pending_lifecycle_deliveries` on `GET /v1/observability/claim-producers/{name}` returns
  `observability.PendingLifecycleDelivery` — seq, scope kind, scope id, event, staged time, attempt count, next
  attempt time, last error — and no payload. Stage 4's answer on bounding: `ListPendingForService` takes a limit and
  pushes it into SQL, both routes that read it pass `persistence.DefaultServiceOutboxPageSize` (100), and the
  producer-outbox route caps its `entries` at the same number while `depth` keeps reporting the true count. The
  dispatcher's `ListAll` is left alone — it is the dispatcher's working set, not a route's page (D32).
- S3-3 — the four `ListTerminatedWithLifecycleRows` methods are deleted from `fixedInstanceTable`,
  `nilInstancesTable`, `fakeInstancesDeps`, and `fakeInstancesForEnqueue`.

### Stage 2 — reviewer findings S2-1 … S2-4

- S2-1 — `LifecycleOutboxTable.ListOldestPendingPerStream` takes a `dueAt` time and both drivers filter
  `next_attempt_at <= dueAt` on the head rows, outside the per-stream subquery and before the `LIMIT`; the drain
  passes its clock's `now`, and the Go loop's after-the-fact due-ness filter is gone.
  `TestLifecycleDrain_ABatchOfBackedOffStreamsDoesNotStarveALiveOne` stages a whole batch (`lifecycleDrainBatch`)
  of distinct failing streams, fails them all once, then stages one live stream and shows the next pass delivers
  it. A persistence conformance assertion proves the predicate on both drivers.
- S2-2 — the unified path builds one drain and shares it. `config.StartSharedLifecycleDrain` dials one subscriber
  registry, constructs one `runtime.LifecycleReconciler`, and runs it; `launch.StartUnifiedStack` starts it and
  hands it to all three role runners through the new `launch.RoleOptions`, which also carries the bundled
  registrations the supervisor and control-api runners took as a separate parameter. Each role config gains
  `SharedLifecycleDrain`: when it is set the role neither constructs, runs, nor stops a drain of its own, and the
  scheduler and supervisor no longer dial a lifecycle registry at all. A per-role process leaves it nil and keeps
  its own drain (D20).
- S2-3 — the two kinds are `LIFECYCLE.ATTEMPT.UNRECORDED` and `LIFECYCLE.DELIVERY.RESCHEDULED`. No lint this
  sprint admits an underscore inside a segment; Stage 6's lint takes the same form.
- S2-4 — D5's second half is rewritten to name each test's instrument after the Stage 1 fix round.

### Stage 1 — reviewer findings S1-1 … S1-7

- S1-1 — Postgres `MergeDelta` reads the bag `FOR UPDATE`, so the read-merge-write is atomic against a
  concurrent merge the way `jsonb ||` was. The debug-override path now takes the same node-run row lock the
  attribute-writeback path takes (`lockNodeRunForAttributeWrite`, the existing
  `Nodes().GetRunByDispatchIDForUpdate` surface) before it reads or merges the bag, so an operator override and
  an executor writeback on one run serialise.
- S1-2 — `MaxValueBytes` is 1,000,000,000; `TestCheckValueSizeRefusesBeforeEitherEngineRefusesAndNamesWhatItRefused`
  replaces the old refusal test and drives sizes past SQLite's cap, past Postgres's, and past both, asserting
  the error wraps `ErrValueTooLarge` and names the run, the value, and the byte count. D1 records what stands.
- S1-3 — `TestScheduler_Start_DefaultsNilClock` becomes
  `TestScheduler_Start_DefaultsNilClockSoTheClockGatedSweepStillRuns`: it seeds a running frame with an
  undelivered triggering message and waits for the delivery that `tick` performs only when `cfg.Clock` is
  non-nil, so deleting the defaulting in `Start` stops the test reaching its outcome.
- S1-4 — `tidy-docker` walks the four modules (D10).
- S1-5 — D10 and D11 record the two out-of-item changes.
- S1-6 — the cross-role read-back failure message claims only what the read-back shows; the one-process claim
  stays `assertSingleRimskyProcess`'s.
- S1-7 — the conformance Dockerfile's header comment lists `lifecycle-subscriber` beside the LABEL.

## Divergences

D1 — **The engine cap is checked in rimsky's writer, and the constant is the smaller engine's true cap.**
`decision:attribute-bytes-in-the-row` says a write over the engine's per-value cap "fails at the write with an
error naming the node run, the attribute or scratch, and the byte count", and forbids a rimsky-side threshold
*below* the engine's. The sprint does not say where the check sits. I put it at the writer as
`persistence.CheckValueSize`. Reading the ceiling back out of a driver-specific error string would make the
message depend on the driver and could not be proven without writing a gigabyte; checking at the writer makes
the error identical on both drivers and testable. The reviewer showed the first constant (`1 << 30`) sat at
Postgres's field ceiling and above SQLite's, so no driver ever reached rimsky's error first. What stands after
the fix round: `MaxValueBytes` is 1,000,000,000 — SQLite's `SQLITE_MAX_LENGTH`, the smaller of the two engines'
caps — so rimsky's named error fires ahead of either driver's raw one, and the tests exercise both engines'
ceilings. The competing reading, dropping the check, was not taken: without it a caller sees a driver error
naming neither the run, nor the value, nor the byte count, which is the one thing the decision promises. The
number is still not rimsky's own — it is the smaller engine's.

D2 — **`MergeAttributeBag` is shared between the two drivers.** Postgres merged an attribute delta with the
`jsonb ||` operator, which a `BYTEA` column cannot use, so both drivers now read-merge-write. That is
semantically identical logic at two sites, so it lives once in `lib/foundation/persistence`. The sprint named
neither the operator nor the helper.

D3 — **The run directory no longer holds `blobs/`.** The amended `decision:artifact-layout` names only the run's
state database, so `EnsureRunDir` stops creating the directory, the compose verb's synthetic config stops
naming a filesystem blob root, and the tests that asserted the directory go. The work item did not name the
compose verb.

D4 — **`RunScheduler` / `RunSupervisor` / `RunControlAPI` lost their pre-opened-backend parameter.** With no
backend to open once and share, the parameter carried nothing, so `StartUnifiedStack` and the three role mains
call the launchers without it. The amended `decision:launch-integration` describes the launcher with no such
parameter.

D5 — **Two scheduler tests changed instrument.** `TestScheduler_Tick_NilClockDoesNotPanicAndStillSweepsOrphanBlobs`
and `TestScheduler_Start_DefaultsNilClock` both proved nil-`Clock` tolerance by watching the orphan-blob sweep.
The sweep is gone, so each proves the same behavior on a different instrument. The first, renamed
`TestScheduler_Tick_NilClockDoesNotPanic`, calls `tick` with a nil `Config.Clock` and asserts it does not panic.
The second, renamed `TestScheduler_Start_DefaultsNilClockSoTheClockGatedSweepStillRuns` in the Stage 1 fix round
(S1-3), seeds a running frame with an undelivered triggering message and waits for the delivery that `tick`
performs only when `cfg.Clock` is non-nil, so deleting the defaulting in `Start` stops the test reaching its
outcome.

D6 — **The all-in-one story test changed what it drives.** `story:single-process-all-in-one` no longer promises
a shared in-process blob map, so `TestSingleProcessAllInOne_MemoryBlobAcrossRoles` becomes
`TestSingleProcessAllInOne_OneProcessServesAllThreeRoles`: it keeps the one-process / no-role-children
assertion and drives a large attribute payload written by one role and read back through another, dropping the
orphan-sweep log waits.

D7 — **`README.md` was edited alongside `docs/`.** The work item named `CLAUDE.md` and `docs/`. The README's
inertness section listed "blob content" as one of the carrier streams and named "blob content inert" as one of
three source-locked properties — both false once the stream is gone, and both contradicting the amended
`concept:inertness`. I corrected the two sentences and left the unrelated typed-attribute `blob` mention alone.

D8 — **The design-corpus TOCs were hand-edited.** `.ok-planner/design/concepts.md` and `decisions.md` say they
are auto-generated, but `.ok-plumbline/bin/catalog-toc` regenerates only the subject and practice catalogs, and
their one-line summaries are curated condensations rather than a mechanical first sentence. I edited the touched
lines in place: removed the seven retired slugs, added `attribute-bytes-in-the-row` in alphabetical order, and
rewrote the summaries for `artifact-layout`, `scratch-column`, `single-process-mode`, and `conformance`.

D9 — **`TransientParkSignalPayload.scratch_spilled` was retired rather than left set to `false`.** The project's
rule is that a field nobody sets should not be declared, and nothing computes a spill any more. The field number
and name are reserved so no future field reuses them.

D10 — **`tidy` walks all four workspace modules, and `tidy-docker` with it.** The retired
`scratch_spilled` proto field made `lib/foundation` a direct `google.golang.org/protobuf` consumer, and root
`go mod tidy` alone leaves the other three modules' manifests behind. The `tidy` target now tidies the root,
`lib/foundation`, `lib/protocols`, and `lib/services`; the reviewer's finding S1-4 carried the same change into
`tidy-docker`, which had walked the root alone. No work item named the Makefile.

D11 — **`docs/config.md`'s driver-comparison paragraph was rewritten, not just trimmed.** The work item said to
remove the blob-backend rows and sections from `docs/`. That paragraph's only concrete example of a
settings-for-settings difference was `persistence.blob.backend: pg-largeobject`, so deleting the example left a
claim with nothing behind it. I rewrote the paragraph around what survives — the SQLite driver's boot warning
and the shared-file warning outside the all-in-one — and dropped the blob example with the section.

D12 — **Only the staged-outbox drain moved to `lib/runtime`; the terminated-instance pass stayed behind.** The
work item says "the reconciler moves from the control layer to `lib/runtime` … and each of the three roles runs
one over the same outbox". The old `controlapi.LifecycleReconciler` did two jobs: the staged drain and a
terminated-instance pass that scans instances and fans out directly through control-layer functions
(`CloseAndFanOutRunScopesForInstance`, `FanOutInstanceEvent`). Only the first is a drain over the outbox, and
running the second in three roles would triple a direct fan-out. The next stage's "Instance terminated staged at
the transition" item deletes that pass outright. So the drain moved and the pass stayed in the control layer as
`controlapi.TerminatedInstanceReconciler`, its own loop in the control-api role alone, to be deleted in Stage 3.

D13 — **The stall-signal decision is not applied yet, so this stage's code cites at-least-once delivery.** The
`service_delivery.stall_after` key and the backoff cap land now because the work item says so, but
`decision:service-delivery-stall-signal` is Stage 4's delta (it also promises event-log kinds and a diagnostics
route this stage does not build). The citation lint refuses a slug with no file, so the outbox columns,
`RecordAttempt`, the backoff, and the config key cite `decision:lifecycle-subscriber-at-least-once-delivery`,
whose Choice already carries "retries a failed delivery on a widening interval". Stage 4 lands the decision and
re-points these sites.

D14 — **The event enum gains `EventRunScopeTerminal` and the drain delivers run-scope rows now.** The work item
asks for the run-scope staging wrapper this stage; a wrapper whose rows no drain can deliver would be a stub. So
the enum, the payload, `DispatchEvent`, and the drain all handle the run-scope scope kind now, and Stage 3 wires
the frame engine and the supervisor to call the wrapper. The direct run-scope fan-out is untouched and stays
until Stage 3 removes it.

D15 — **`PassesCompleted()` on the drain.** A test that proves the kick wakes the drain has to know the drain
finished its opening pass, or it cannot tell a kick from that pass. The scheduler already exposes
`TicksCompleted()` for the same reason (`decision:polling-audit`), so the drain exposes the same shape and the
kick test waits on it rather than on a duration.

D16 — **The retry backoff is shared with the producer-verb dispatcher.** Both outboxes widen a retry the same
way, and copying the doubling loop would be two definitions that must agree. `outboxRetryBackoff` is the one
definition; `producerVerbBackoff` keeps its own defaults and calls it.

D17 — **A staged row's due time comes from the wall clock that stamped it, not from an injected clock.**
`LifecycleOutbox.Stage` defaults `staged_at` (and now `next_attempt_at`) to `time.Now()`, as it always has;
threading a clock through every staging call site is a change the sprint does not ask for. The drain compares
against its own injected clock, so a test that drives the drain's clock starts it past the rows it stages.

D18 — **`docs/config.md` gained a `service_delivery` section.** The work item named the configuration key, not
the tree's documents. An operator cannot find a shipped key that the config reference omits, and Stage 1 already removed
from `docs/` the keys this sprint retired.

D19 — **The SQLite migration backfills `next_attempt_at` with the epoch.** SQLite's `ALTER TABLE ADD COLUMN`
admits only a constant default, so a row staged before the migration cannot inherit `datetime('now')`. The epoch
makes every pre-existing row due at once, and every such row has already been waiting.

D20 — **A role process that owns its drain still dials a lifecycle subscriber registry.** The work item says "the
scheduler's and supervisor's subscriber registries and subscriber-list configuration go" and "the supervisor no
longer dials lifecycle subscribers at all", and in the same paragraph says each role runs its own drain and kicks
it. A drain with no registry delivers nothing, so the two sentences cannot both hold literally for a split
deployment. What went: the registry and the subscriber-list closure on the *dispatch* path — `RunArgs`,
`CallbackServer`, `runtime.Config`, `scheduler.Config`, `SchedulerConfig`, `SupervisorConfig` — so neither role
calls a subscriber directly any more. What stayed: `config.StartScheduler` and `config.StartSupervisor` dial one
registry for their own drain, and only when they own one. In the all-in-one deployment they own none, so neither
dials, and the sentence holds there exactly.

D21 — **The scope-kind enum is renamed off the deleted ledger.** `LifecycleIdempotencyScopeKind` and its three
constants named a table that migration 048 drops, and they are the outbox row's and the advisory lock's own
vocabulary. They are now `LifecycleScopeKind` and `LifecycleScope{Template,Instance,RunScope}`, declared beside
the outbox row. The sprint named the accessor and the state enum, not this one; leaving it would have kept a live
type pointing at a table that no longer exists.

D22 — **The delivery re-reads its row inside the advisory lock.** The ledger's read-inside-the-lock was what made
two drains racing on one row converge to one delivery. With the ledger gone, `DeliverStagedLifecycleRow` re-reads
the row by seq inside the lock (the new `LifecycleOutboxTable.GetBySeq`) and returns without dispatching when it
is already gone. The sprint says the lock's critical section is "the service call and the row delete"; the
re-read is what makes that section mean anything, and `TestStagedDelivery_TwoReplicasSharingOneDBDeliverOneStagedRowOnce`
holds the proof the deleted `TestFanOutRunScopeEvent_TwoReplicasSharingOneDBDeliverExactlyOnce` carried.

D23 — **A frame settlement stages a terminal only for the scopes it closes.** `closeSettledFrameScopeTree`
returned every scope in the tree, closed or not, and the direct fan-out relied on the ledger to swallow the
repeats. Without the ledger that shape would deliver a second terminal for every child scope the supervisor
already closed at rendezvous. It now returns the scopes the transaction itself closes, and the terminate route's
equivalent does the same, which is what the sprint's own "for each scope the termination closes" asks for.

D24 — **The claim-producer observability route reports pending outbox rows.** `GET /v1/observability/claim-producers/{name}`
returned a `lifecycle` array of ledger rows, so deleting the ledger forced the route to change now rather than in
Stage 4. It returns `pending_lifecycle_deliveries` — what that service still owes — read through a new
`LifecycleOutboxTable.ListPendingForService` that Stage 4's diagnostics route reuses. Stage 4 owns the rest of
that item.

D25 — **Stage 3's new outbox surfaces cite `concept:lifecycle-subscriber`, not the stall-signal decision.**
`decision:service-delivery-stall-signal` is Stage 4's delta and the citation lint refuses a slug with no file, so
`ListPendingForService` and its call sites cite the concept that owns the outbox. Stage 4 re-points them, as D13
already records for the Stage 2 sites.

D26 — **Five documents under `docs/` were corrected.** No Stage 3 work item names `docs/`, and the record
discipline lets a release snapshot go stale. These five sentences did not go stale, they went false about code
this stage wrote: `docs/grpc.md` and `docs/protocols/lifecycle-subscriber.md` described the delivery ledger, the
synchronous firing, and a subscriber's refusal blocking the operation; `docs/concepts.md` said rimsky tracks
idempotency per service, event type, and object; `docs/examples/lifecycle-subscriber-author.md` said
`OnInstanceTerminated` arrives on the delete; `docs/http-api.md` said the delete route fans the event out and
named the removed `lifecycle` response field. Stage 1 set the precedent (D7, D11). The next `/document` run
revises the rest.

F1 — **Ruled: the delete route stages no second `instance_terminated`; reading (b) stands.** The architect
overturned the fork. The work item's clause names the transaction that stamps `terminated_at`.
`handleDeleteInstance` never stamps it: the route answers 409 unless `terminated_at` is already set. The clause
therefore names the terminate route alone. The promoted issue
`instance-delete-drops-undelivered-lifecycle-events` rules the same point in its own words: stage
`instance_terminated` inside the termination transaction, and leave the outbox rows for the drain to deliver.
`TestCanary_LifecycleSubscriberCallbackContract` predates this sprint and holds the protocol to exactly one
`OnInstanceTerminated` across a terminate-then-delete sequence. No reasonable owner licenses a duplicate that a
ruling and a standing contract test both refuse. The built reading stands: the delete route closes any run scope
still open, stages a terminal for each, stages no second instance event, purges nothing, and delivers inline after
commit. The architect changed no file and filed no issue.

D27 — **Six tests that read the attribute bag through raw SQL were repaired.** Running the whole
`./test/...` tree turned up five failures in `test/scenarios` and one unrunnable query in
`lib/services/test/scenarios`, all from Stage 1's byte columns: `rimsky_node_attributes.data` and
`dispatch_input_bag` are `BYTEA` now, so `data::text` returns a hex escape, `data ? 'key'` and
`jsonb_set(data, ...)` have no operator, and `COALESCE(na.data, '{}'::jsonb)` has no common type. The queries
read the bag through `convert_from(..., 'UTF8')` (and write it back through `convert_to`), which is what the
column holds. Stage 1 ran the two scenarios it had touched rather than the tree, so these went unseen until
this stage ran the suite; the tests themselves are unchanged in what they prove.

D28 — **No migration for the producer-verb outbox's failure state.** The work item says "the producer-verb outbox
gains the same three columns where it lacks any". It lacks none: `attempt_count`, `next_attempt_at`, and
`last_error` are already on `rimsky_producer_verb_outbox` with a `RecordAttempt` writer and a diagnostics route
that reads them. Migration 049 therefore carries only the new stall-marker table.

D29 — **The stall edge is a persisted marker, not a remembered set.** The work item offers either. A remembered
set lives in one process, and `decision:lifecycle-drain-per-role` puts a drain in each of the three roles over one
outbox, so a split deployment would write the stall entry up to three times — which is exactly the per-attempt
volume the decision rejects. The marker table makes the edge a database fact: `MarkStalled` is
insert-on-conflict-do-nothing and `ClearStalled` is a delete, each reporting whether it changed the row, so
whichever drain gets there first writes the entry and the others write nothing. It costs one small table
(`rimsky_service_delivery_stalls`, keyed `(service, outbox)`) and one migration, and it survives a restart, which
a remembered set does not.

D30 — **The config loader refuses a retention window that is not longer than the stall threshold.** The amended
`decision:lifecycle-subscriber-at-least-once-delivery` says the stall signal "makes the failure visible before the
window discards it". Nothing made that true: with `stall_after: 1h` and `lifecycle_outbox_trailing: 30m` the sweep
would delete an undelivered row half an hour before anything reported it. Every written constraint needs a check
that fails on violation, so `checkStallSignalPrecedesRetention` rejects that pair at load, naming both keys. The
shipped default (window unset, nothing discarded) is unaffected, and the sweep itself is unchanged — this was the
reviewer's Stage 4 note on `SweepLifecycleOutbox`, and the sweep needed no behavior change beyond the guard.

D31 — **Five documents under `docs/` gained the new surface.** No Stage 4 work item names `docs/`. Two of the
edits repair enumerations that this stage made incomplete — `docs/permissions.md`'s route list for
`diagnostics:read` and `docs/concepts.md`'s two exhaustive lists (every route, every event kind) — and the rest
document a shipped route (`docs/http-api.md`, `docs/operating.md`) and a shipped key whose accepted values
narrowed (`docs/config.md`). Stages 1 and 3 set the precedent (D7, D11, D26). The next `/document` run revises
the rest.

D32 — **Only the read paths that serve a route are bounded.** The reviewer left Stage 4 to decide whether to bound
`ListPendingForService` and its unbounded sibling `ListAll`. `ListPendingForService` now takes a limit pushed into
SQL, and both routes reading it pass 100. `ListAll` stays unbounded because it is not a route's page: the
producer-verb dispatcher needs every row to run its per-scope head-of-line barrier, and truncating it would drop
deliveries. The producer-outbox route caps the entries it serialises at the same 100 while `depth` reports the
true count, so neither route can be asked to serialise a million rows.

D33 — **The two new process-log kinds take the standard's form; the pre-existing ones nearby do not.** Stage 6
owns the whole process-log rename, so this stage left every pre-existing literal alone, including
`lifecycle_reconciler.staged_delivery_list_failed` two functions away. The two literals this stage wrote —
`LIFECYCLE.PENDINGSUMMARY.UNREAD` and `SERVICEDELIVERY.HEALTH.UNRECORDED`, the one kind the fix round's
single-transaction health pass emits for a stall or a recovery it could not record — take `SUBSYSTEM.NOUN.VERB`,
as Stage 2's two new kinds did.

F2 — **Ruled: a stalled service recovers when nothing it owes is past the threshold; reading (b) stands.** The
architect overturned the fork. `decision:service-delivery-stall-signal` calls the pair an edge: "the signal is the
edge, not the attempt". The promoted issue `event-log-domain-for-peer-delivery-health` names the same option
"stall/recover edge kinds", written "when a peer's delivery first stalls and when it recovers". A recovery is
therefore the stall predicate negated, not any successful delivery. Take a service with one blocked stream and one
flowing stream. Reading (a) writes a `recovered` and a `stalled` on every pass over it, which is the per-attempt
volume the decision's own rationale rejects, and each `recovered` reports a recovery that did not happen. No
reasonable owner adopts a reading their own rationale forecloses. The built reading stands. The architect changed
no file and filed no issue.

D34 — **`lib/runtime/peer` became `lib/runtime/service`.** The sprint names one package by hand,
`lib/protocols/peerauth`, and then states the rule: every identifier that says "peer" for a deployed service
says "service". This package is the runtime's client side of the service protocols — the dial, the credentials,
the identity holder, the per-protocol clients — so the rule reaches its name. `serviceclient` was the competing
name and was not taken: the sweep's own output reads correctly at every call site (`service.Dial`,
`service.TLSModeOff`, `service.ProducerCallError`), and no file that imports it declares a `service` identifier
that the package name would shadow, which the build and vet over all four modules confirm.

F3 — **Ruled: the pending-delivery field belongs on the service-status route of every service kind; reading (b)
stands, and the architect landed it.** The architect overturned the fork. The sprint names one route and its
rename, not a new route. `GET /v1/observability/peers` never existed, and neither did the CLI verb the sentence
pairs with it, so the sentence describes the service-status surface the tree does have: the per-kind detail routes
`GET /v1/observability/executors/{name}` and `GET /v1/observability/claim-producers/{name}`. Option (a) reads a
mistaken premise as licence to invent a public route the sprint never planned and the surface intent does not
classify. Its one advantage is a single place to read what every service is owed, and
`GET /v1/admin/diagnostics/lifecycle-outbox` already serves that — `decision:service-delivery-stall-signal` names
that route as the one answering what is owed right now. The built state did leave an asymmetry:
`concept:lifecycle-subscriber` says any service kind may subscribe, so an executor that subscribes is owed
deliveries, and the claim-producer route reported them while the executor route did not.
The architect made the fix. `writeServiceStatus` in `lib/control/observability/handler.go` is now the one body
behind both detail routes, so each reports `service` and `pending_lifecycle_deliveries`.
`TestHandler_ServiceStatusReportsWhatASubscribingServiceIsOwed` stages a failed delivery for an executor and for a
claim producer and reads both routes back; it is the first test over this field on either route.
`docs/http-api.md`'s executor row now carries the response shape. The architect ran `go build ./...`,
`go test ./lib/control/observability/... ./lib/control/controlapi/... ./test/plumbline/... -count=1`, `make lint`,
and the plumbline lint over the touched files; all passed. The architect filed no issue.

D36 — **The sprint's `### Amend concept: host-agent-proxy` heading points at a sidecar file that does not
exist.** The vocabulary sweep's own `### Retire concept: host-agent-proxy` / `### New concept:
host-daemon-proxy` pair does have a body, and that body carries the clause the amendment was for: each
lifecycle notification arrives through the outbox, so the reap of a closed run scope's spawns follows the
scope's close by the drain's delivery and is retried when the proxy was unreachable. Applying
`concepts/host-daemon-proxy.md` therefore lands the amendment too; no body was written to fill the gap.

D37 — **The design TOCs were hand-edited again.** D8 records why. This stage rewrote every touched summary in
the new vocabulary, moved `service-auth` into alphabetical order among the `service*` entries, and re-sorted
each list with the comparator the files already use (a slug's hyphenated extensions sort before the bare slug).
The comparator reproduces `concepts.md` and `decisions.md` unchanged; in `stories.md` it also swaps
`claim-handoff` and `claim-handoff-durable`, which were out of order before this sprint.

D38 — **"Agent" survives in more places than the sprint's two-clause exemption names.** The sprint exempts the
claude-agent executor and the agentic-tool surface. Read strictly that would rename four more populations that
mean an LLM agent, so each was kept: the bundled permission role `agent-supervisor` and `debug-operator`'s
"agent keys" (a role for a supervising LLM agent, not a host daemon); the `agent/*` error-class family and
`docs/errors/agent-classes.md`; `README.md`'s framing of rimsky as an agent orchestrator, which the sweep
rewrote and which was restored whole; and the HTTP `User-Agent` header. The sweep protected
`claude-agent`/`ClaudeAgent`, `user_agent`, `agentic`, the error classes, and the LLM-agent identifiers in the
claude-agent package by pattern, and the residue was read line by line.

D39 — **`lib/services/internal/agentport` became `daemonport`.** The sprint names `RIMSKY_AGENT_PORT` and the
`lib/runtime/hostagent` package. The variable's other reader lives in the services module, in a package named
for it, and is what a bundled executor calls to honour the port the daemon assigned. It follows the variable.

D40 — **Stale port numbers in `docs/` were corrected.** The sweep rewrote the sentences naming the proxy's two
listeners, and those sentences said 9090 and 9091 while `code:lib/foundation/ports` and the default-port fitness
check say 8090 and 8091 — the work item puts the default-port audit inside this rename. Correcting them made
three "default ports collide" paragraphs false in the other direction, because the supervisor's callback
listener is 8081 (both `ports.SupervisorCallback` and the port `dockerfiles/all-in-one.rimsky.yml` bakes), not
9100. Those paragraphs now state what the fitness check proves — no two shipped defaults coincide — and the six
other sentences carrying 9100 as the callback port name 8081. No behaviour changed and no example that sets a
port explicitly was touched.

D41 — **`releases/*.md` were left alone.** They describe what shipped at a past version, in the vocabulary that
version shipped with. Rewriting them would falsify the record. The same rule left `LICENSE.agpl`, whose
"peer-to-peer" is the licence's own text. `.ok-planner/audits/`, `.ok-planner/experiments/`, and
`.ok-planner/documentation/` were left untouched as the sprint's execution rules require, so the experiment
directories still carry the retired story slugs until the next `/audit` run.

D42 — **An example's service name was chosen rather than swept.** `docs/examples/audit-artifact.md` named its
late-bound executor `peer`, which the sweep would have turned into `--service service=/path/to/service-host`.
It is `local-executor` now.

D43 — **The fixed TLS server name changed.** `enroll.PeerServerName = "peer.rimsky.internal"` is
`enroll.ServiceServerName = "service.rimsky.internal"`, so the deployment CA stamps the new name into every
leaf and every verifier checks for it. Both sides are in this tree, and pre-v1 nothing outside it holds a leaf;
a deployment running under mutual-TLS service auth across this change re-enrols.

D44 — **Two operator-facing names outside the work item's enumeration moved.** The work item enumerates the
binary, the image, the Dockerfile, the CLI verb, the environment variables, the package, the listener, the
protocol, the log kinds, and the error vocabulary. Two more names said "agent" for the host daemon and had to
move with them, or the rename would have left a caller writing `target_agent` to reach a daemon: the
instance-create JSON body field `target_agent` is `target_daemon`
(`code:lib/control/controlapi/instances.go::createInstanceBody.TargetDaemon`), and the CLI flag `--agent` on
`instance create` and `instantiate` is `--daemon` (`code:cmd/rimsky/cli/instances.go`). Both are breaking
changes to the control surface, which pre-v1 licenses.

D45 — **The proto rename changes a gRPC full method name, and that is this sprint's largest outward break.**
`HostDaemon_Connect_FullMethodName` is `/rimsky.v1.HostDaemon/Connect`, where it was
`/rimsky.v1.HostAgent/Connect`. A daemon built against the old binding cannot reach a proxy built against the
new one, and the failure is an `Unimplemented` at dial time rather than a compile error. Both ends of this
protocol ship in this tree — the daemon inside the `rimsky` binary, the proxy in its own image — so a
deployment upgrades them together; nothing outside the tree implements it. Pre-v1 licenses the break. The
message names, the two `Register` field names, and the `host_daemon_not_connected` /
`host_daemon_disconnected` error classes move with it.

D46 — **`docs/env-vars.md` carried four rows no code backs.** It also carried one false claim about the
egress guard. The fix round's audit of the table's "Default" column against the code found: `RIMSKY_DAEMON_TLS`, which no
code reads (the dial is TLS by default and `RIMSKY_HOST_DAEMON_INSECURE` is the plaintext switch);
`RIMSKY_DAEMON_TLS_CA` documented as required, where it is the optional pin; `RIMSKY_DISPATCH_MAX_USD` and
`RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL`, which the code reads under their `RIMSKY_CLAUDE_AGENT_`
names; and `RIMSKY_SUPERVISOR_CONFIG`, which nothing reads at all. The table also omitted three variables the
code does read. Every row is now what the code holds, and the two claude-agent names were corrected at their
other four sites under `docs/`. Two sentences claiming the egress guard covers `http-node` and `sensor-http`
alone were false — `code:lib/services/executors/verifier-http/opts.go` builds a guard from
`RIMSKY_EXECUTOR_VERIFIER_HTTP_EGRESS_ALLOWLIST` and `executor.go` dials the node-supplied `url` through it —
so the guard covers three services in `docs/env-vars.md` and in `CLAUDE.md`'s gotcha.

D47 — **The separate supervisor configuration file the documents describe does not exist, and every site that
described one is repaired.** Supervisor tuning lives in the unified configuration file's `supervisor:` section
(`code:lib/control/config/claim_producers.go::yamlSupervisor` — `supervisor_id`, `concurrency`,
`liveness_interval_ms`, `claim_poll_interval_ms`, `callback`), which is what `dockerfiles/all-in-one.rimsky.yml`
sets and what `concept:rimsky-yml` says. Nothing reads `env:RIMSKY_SUPERVISOR_CONFIG`. The Stage 5 fix round
removed its `docs/env-vars.md` row and named the rest here rather than half-doing them; the Stage 6 round did
them: `docs/config.md` (the file table, the "three kinds of configuration file" sentence, a new `### supervisor`
subsection under `## rimsky.yml` in place of the top-level `## Supervisor config` section, and the top-level key
count from twelve to fourteen), `docs/operating.md` (one file, fourteen keys), `docs/images.md` (one mounted
file, the `supervisor:` section named in it, and the all-in-one paragraph), `docs/concepts.md` (the `rimsky-yml`
and `supervisor` entries and the image-path enumeration, now three), and
`docs/cookbook/journey-split-roles-postgres.md` (the second YAML block folded in as a `supervisor:` section on
port 8081, and one mounted file). No `supervisor-config` string is left under `docs/`.

D48 — **The subsystem vocabulary is one first segment per bounded area of the tree, not per Go package.** The
sprint and `decision:structured-log-kind-format` fix the shape `SUBSYSTEM.NOUN.VERB` and name no subsystem set.
A segment per Go package would put `RUNNER`, `CASCADE`, `RUNTREE`, and `CALLBACK` all under `LIB.RUNTIME`, which
tells an operator nothing; a segment per binary would collapse the whole engine into `RIMSKY`. I settled the
first segment on the area a reader filters by. Core: `ENTRYPOINT`, `LAUNCH`, `PROCESS`, `MIGRATE`, `PERSISTENCE`,
`CONTROLAPI`, `CONTROLAPIMCP`, `SCHEDULER`, `SUPERVISOR`, `CONDUCTOR`, `FRAME`, `RUNNER`, `RUNTREE`, `CASCADE`,
`SUBGRAPH`, `CALLBACK`, `KEEPALIVE`, `LIFECYCLE`, `PRODUCERVERB`, `PUBLISHER`, `INSTANCE`, `INSTANCEKILL`,
`TEMPLATE`, `ATTRIBUTEOVERRIDE`, `ATTRIBUTEWRITEBACK`, `AUTH`, `SERVICEAUTH`, `AUDIT` (folded into `CONTROLAPI`),
`BREAKPOINT`, `CLAIMHANDLE`, `CLAIMPRODUCERREGISTRY`, `ORPHANREAPER`, `PARKEDNODE`, `MESSAGE`, `LINEAGE`,
`METRICS`, `OBSERVABILITY`, `RETENTION`, `SIGNAL`, `MATCHER`, `BUNDLED`, `PROXY`, `HOSTDAEMON`, `COMPOSE`,
`SERVERKIT`, `PUBLISHERKIT`. One per shipped service: `CLAUDEAGENT`, `CLAUDEAGENTMCP`, `HTTPNODE`,
`VERIFIERHTTP`, `VERIFIERSHAPECHECKS`, `SENSORCRON`, `SENSORHTTP`, `SENSOROBJECTSTORE`, `SENSORWEBHOOK`,
`FILESYSTEMSTORE`, `POSTGRESSTORE`, `OPENLINEAGE`. Test-only: `HARNESS`, `STUBEXECUTOR`, `OVERLAPPRODUCER`,
`TEST`. Underscores inside a segment are joined out, as Stage 2's `LIFECYCLE.PENDINGSUMMARY.UNREAD` already had
it, because the standard admits letters and digits alone.

D49 — **Prose is preserved where the kind cannot carry it, and dropped where it can.** The work item says a
literal that carried prose "keeps that prose in a field". Read as "keep every sentence", every one of the 588
sites would carry a `detail` field restating its own kind — `CALLBACK.POST.FAILED` beside `detail: "callback POST
failed"` — which is padding, not information. I read it as the information rule it is: where the kind names what
the sentence said, the sentence goes; where the sentence carried a consequence no kind can hold — "the kill
stands", "the row may stay claimed until the liveness sweep", "the leaf candidate_handle is left empty",
"skipping the candidate" — that clause rides a `detail` field, rewritten as a standalone sentence rather than a
trailing fragment. 132 sites carry a `detail`. Nothing a literal said is lost.

D50 — **A Go symbol in a literal became a `site` field, and that field is what lets one kind serve several call
sites.** The work item asks for it, and the payoff is bigger than the rename: nine call sites logged
`<funcName>: run-tree propagation failed`, and they are one event. They are now one kind,
`RUNTREE.PROPAGATION.FAILED`, with `"site", "<funcName>"` telling the operator which caller hit it. The same
holds for `RUNNER.EXECUTORSCHEMA.UNMARSHALFAILED` (two sites), `CLAIMHANDLE.*` (four), `INSTANCEKILL.*`, and
`METRICS.GAUGEREFRESH.FAILED` (four accessor names). A kind is unique in meaning across the tree; `site` is the
field that keeps it that way instead of minting a near-duplicate kind per caller.

D51 — **A message the lint cannot read statically is a violation, and a kind a caller already named is checked at
that caller.** The brief left this to me. An unreadable message is `unreadable-log-kind`, and the baseline cannot
record it: a site with no literal has no kind to count. I rewrote twenty such sites, each into a kind plus a field
that carries what varied (`w.verb` → `"verb"`, `serviceName` → `"service"`, `label` → `"service"`, `site` →
`"site"`, an iterated warning string → `"detail"`). Four forwarding shims remain, and each passes through a kind
its caller named rather than naming one: `func (s *slogLogger) Info(msg string, ...)`, the four sensors'
`slogAdapter`, `logFanOutFailures(deps, msg, ...)`, `retryRPCWithBackoff(ctx, log, logEvent, ...)`, and
`sharedLoggerHandler.Handle` forwarding `r.Message` from the `slog.Record` it was handed. A shim does not own the
kind it forwards, so the scan follows the kind to the caller instead of stopping at the shim. It records every
receiver-less function that forwards a parameter into a kind position, together with that parameter's index, and
reaches a chain of such functions by fixpoint. It then checks the argument at that index in every call to one —
an ident call in the same package directory, or a qualified call whose import path ends in that directory — under
the same rule it applies to a logger call. A malformed literal handed to a forwarder is `malformed-log-kind` at
the call site, and an unreadable argument is `unreadable-log-kind` there. Mutating `logFanOutFailures`'s and
`retryRPCWithBackoff`'s call sites back to the old literals proves both. A forwarding *method* needs no such
reach, because its callers hold a logger-typed receiver and the scan already reads them as logger calls. The
exemption binds to the parameter's declaration rather than its name. `parameterIndex` records every declaring
identifier in a file with the extent of the block that declares it, and a use resolves to its innermost binding.
A local that shadows a parameter of the same name is therefore no pass-through, and its site is a violation. The
resolver reads declarations rather than `ast.Object`, which Go 1.22 deprecated and staticcheck refuses.

D52 — **`Log` and `LogAttrs` are read, and the tree emits neither.** The scan takes the message from argument
index 0 for `Debug`/`Info`/`Warn`/`Error`, index 1 for the four `*Context` variants, and index 2 for `Log` and
`LogAttrs`, so a future site in either form is covered. No emit site in the tree uses them today; the only
`.Log(` calls are `testing.T.Log`, which is not a logger receiver.

D53 — **`make lint` runs the lint through a `-check` mode; the fitness test is the suite's gate.** The work item
asks for both a fitness test under `test/plumbline/` and membership in `make lint`, and the wall-clock lint the
new lint is shaped after is wired only as a fitness test. Running the default CLI mode from `make lint` would
have the lint rewrite its own baseline as a side effect of linting, so the binary gained `-check`: it writes
nothing and exits 2 on any violation outside the baseline, the same shape `license-lint` already has in that
target. `make lint` gains `logkind-lint` as a prerequisite beside `license-lint`, and
`test/plumbline/logkind_ratchet_test.go` holds the empty baseline for the suite, matching the wall-clock
ratchet. The wall-clock lint stays out of `make lint`; changing its wiring is no work item of this sprint.

D54 — **The shared logger's own unit test carries kind-form fixtures.** `lib/foundation/shared/logger_test.go`
used `"parent message"`, `"child message"`, and `"grandchild message"` as fixture data proving the capturing
logger forwards a message and its base fields. They are messages at real emit sites, so the lint sees them. I
renamed them to `TEST.PARENTLOGGER.EMITTED`, `TEST.CHILDLOGGER.EMITTED`, and `TEST.GRANDCHILDLOGGER.EMITTED`, and
moved the assertions with them, rather than exempting the file: the gate is absolute with an empty baseline, and
a fixture that reads as a kind costs the test nothing.

D55 — **Two test assertions matched a substring of the old message rather than the whole literal.**
`lib/control/controlapi/instances_static_config_gate_test.go` asserted on `malformed_executor_schema` and
`cmd/rimsky/cli/compose/shutdown_test.go` on `signal while draining`, so neither turned up in the sweep over
whole literals; both surfaced as test failures and now match the renamed kind. A sweep over quoted literals
alone does not find a substring assertion — the package tests are what caught these.

D56 — **Six `docs/` sentences named a process-log message that no longer exists.** No Stage 6 work item names
`docs/`. Four repeated the sentence "the entrypoint reports `migrate failed`" (`docs/config.md`,
`docs/templates.md`, `docs/cookbook/error-routing.md`, `docs/cookbook/journey-split-roles-postgres.md`) and
`docs/examples/local-orchestrator-zero-config.md` told the reader to look for `bundled executor registered
in-process`; each named the exact literal this stage renamed, so each went false rather than stale. They now name
`ENTRYPOINT.MIGRATE.FAILED` and `BUNDLED.EXECUTOR.REGISTERED`. Stages 1, 3, 4, and 5 set the precedent (D7, D11,
D26, D31). The next `/document` run revises the rest.

D57 — **The lint scans `test/` as well.** `decision:structured-log-kind-format` says the lint runs "over the
tree", and the first cut took its scan roots from the wall-clock lint, which reads `cmd`, `lib`, and `tools`. The
out-of-tree support binaries the scenarios drive emit process logs of their own, and three of their literals broke
the form. `scanRoots` now reads `cmd`, `lib`, `test`, and `tools`, and the three literals now read
`STUBSTORE.GRPC.SERVEFAILED` and `STUBSTORE.HTTP.SERVEFAILED`
(`code:test/support/claim_producers/stub/server/server.go`) and `COMPOSESTUB.GRPC.LISTENING`
(`code:test/support/composestub/main.go`). No test waits on the three old literals. Each appeared once, at its
emit site. `STUBSTORE` and `COMPOSESTUB` join D48's subsystem set beside the other test-only segments.

D58 — **One ledger row dissolved before this stage reached it.** The ledger calls
`lifecycle_fanout_after_commit_test.go::TestLifecycleReconciler_RunScopeTerminalPrecedesInstanceTerminated`
redundant with `lifecycle_reconciler_test.go::TestLifecycleReconciler_RowFoundRPCSucceedsRowDeleted`. Stage 3 moved
the reconciler out of the control layer and deleted that file, and rewrote the surviving test as
`TestTerminateInstance_RunScopeTerminalPrecedesInstanceTerminated`, which now carries the sequence assertion plus
the run-scope closure the sibling never had. Nothing remains in the form the ledger recorded, so the row is closed
by Stage 3's rewrite and this stage changes nothing. The alternative — delete the surviving test — would drop the
only proof of the ordering.

D59 — **The claim-producer conformance check takes its reader-open bound as a parameter, not a `Clock`.** The
sprint names four product waits to inject the `Clock` into, and this is not one of them, but the wait that decided
the test's verdict lives in `checkSerialization9b`, which ships to third parties and must bound a remote
producer's `Open` to tell "blocked" from "available". A `Clock` does not fit: the bound is a `context`, not a
sleep. `checkSerialization9b` and `openConcurrentReaders` now take a `readerOpenBound func(context.Context)
(context.Context, context.CancelFunc)`; `Run` passes `boundReaderOpenByTimeout` (the 2 s deadline, unchanged), and
the test passes a context it ends itself once the fake reports both readers parked. The blocked test widened with
it: a reader whose `Open` outlasts its bound now counts as blocked for `Canceled` as well as `DeadlineExceeded`,
and only while the run's own context is still live, so a caller cancelling the whole conformance run is not
reported as a stalled producer.

D60 — **`RunnerOpts` gains a `Scenarios` field rather than a test-only registry seam.** The flagged test appended
to the package-level `registered` slice and popped it in cleanup. The executor conformance runner now takes the
scenario set explicitly, defaulting to `All()` when the field is nil, so the test hands in its own one-scenario
set. Every production caller is unchanged. The alternative — a `registerForTest` helper — keeps the shared slice
and the ordering hazard, which is the finding.

D61 — **The demo script's two deadline polls become unbounded polls, and a third boot bound goes.** The ledger
flags the Go test whose verdict rides `wait_dialable`'s `date +%s` deadline. The same file carried a second
deadline poll (five seconds for the daemon's exit after `stop`) that decides the same test's verdict; the sprint's
rule reaches it as directly. Both now poll without a deadline. The dial poll keeps one real failure — the process
it waits on exited — which is a signal, not a timeout; the exit poll needs none, because `daemon stop` returns only
after it has confirmed the exit. A boot that never answers now leaves the run's no-progress watchdog to stop the
run, which is what the standard asks for.

D62 — **The claude-agent loop takes an injected `now` and ticker, not `shared.Clock`.** The sprint says to inject
the `Clock` here. `shared.Clock` lives in `lib/foundation`, which the `consumption-side-isolation` depguard rule
forbids every shipped `lib/services` package from importing, so the type is unreachable from the claude-agent
executor. The three options were: move `Clock` into `lib/protocols` (which would relicense an AGPL utility into
the Apache module and touch every `shared.Clock` caller), define a second `Clock` inside `lib/services` (a second
dialect for one job), or take the two things the loop actually needs as explicit fields. `AgentRunOptions` now
carries `Now func() time.Time` and `TimeoutTicker func() (<-chan time.Time, func())`, both defaulting to the real
ones — the `now func() time.Time` idiom `lib/services` already uses in `http-node` and the openlineage subscriber.
The loop's `time.Sleep(100ms)` becomes a range over the injected ticker, so the tests fire one tick and observe
the outcome. The other three named waits (agent-stop escalation, control-API readiness, compose child grace) are
in `cmd/`, which may import `lib/foundation`, and all three take the real `shared.Clock`.

D63 — **The daemon-stop escalation takes a process-control value, not just a clock.** Injecting a clock alone
leaves the test racing the operating system: after `SIGKILL` the test would have to advance the clock while the
kernel reaps, and whichever wins decides the verdict. `runDaemonStop` now delegates to `stopDaemon(args,
daemonProcessControl)`, which carries the liveness probe, the two signal senders, the clock, and the two windows;
production builds it from `processAlive` / `terminateProcess` / `killProcess` / `shared.SystemClock{}`. The test
supplies fakes and a virtual clock that advances on `Sleep`, so it proves the SIGTERM-then-SIGKILL order and that
the escalation waited the full grace window on the daemon's own clock, with no real process and no elapsed time.
The two windows change from `var` to `const` with it.

D64 — **`shared.AutoAdvanceClock` joins `ControllableClock`.** A deadline-bounded product loop driven by
`ControllableClock` deadlocks: its `Sleep` blocks until an `Advance` the test cannot schedule without racing.
`AutoAdvanceClock.Sleep` advances the clock by the slept duration and returns, so virtual time moves at the
product's own cadence and the loop reaches its deadline in zero real time. It sits beside `ControllableClock` in
`lib/foundation/shared/clock.go`, where the project already keeps its clock doubles, so there is one clock package
rather than two.

D65 — **The context-deadline detector fires only where the enclosing function names deadline expiry.** The work
item says the lint reads a context deadline "whose expiry feeds a verdict". Reading every `context.WithTimeout` in
test code would flag about seventy sites, nearly all container-boot bounds whose expiry is a real failure, not a
timing verdict. The mechanical reading of the qualifier is: the enclosing function asserts on the expiry
(`DeadlineExceeded` or "deadline exceeded" appears in its body). That found exactly three sites — one of them the
ledger's own row 3991 — and all three are fixed. A deadline the enclosing function never asserts on is not read at
all, which is the same admission the work item grants "a deadline that is the input under test", without needing a
marker; a deadline under test that *does* assert expiry stays admissible with a class marker and a justification.

D66 — **The package-state check reads writes from test functions and `testing.TB` helpers, not every write in a
test-code file.** The work item says "a test that writes a package-level variable". Read as "any write in a file
the scanner classes as test code", it also catches memoised singletons in harness constructors — a per-network
container cache, a lazily built TLS client — which are shared *resources*, not shared verdict state, and which no
restructuring removes. The check therefore reads a write whose enclosing function is a `Test`/`Benchmark`/`Fuzz`/
`Example` function or takes a `testing.TB`-family parameter: literally "a test that writes". Mutation reached
through a package function that writes the variable itself (a registrar called from a test) is not read; that stays
a reviewer's finding.

D67 — **The claude-agent MCP module registry becomes injectable, and `ParseCliConfig` takes the map.** The flagged
test called `RegisterMcpModule` and never deregistered; the package's own `registerTestModule` helper wrote the
same global under cleanup, which the new check reads too. Rather than keep either, `Opts` and `AgentRunOptions`
gain `McpModules map[string]ModuleMcpFactory`, `ParseCliConfig(v, modules)` and `standUpModuleLoopback(name,
specifier, modules, logger)` take it, and `resolveMcpModule` consults the injected map before the package
registry. `RegisterMcpModule` stays for the host binaries that register at init (`fakeclaudeserver`), so no
production path changes. `ParseCliConfig`'s signature change is an exported-surface break, which the pre-v1 rule
admits; every caller in the tree is updated.

D68 — **The services harness's two stub memos move onto a registry type.** `stubLaunched` and `stubErrLaunched`
were package maps written from `testing.TB`-taking functions. They memoise one container per docker network, which
is a shared resource and not a verdict input, so deleting the memo is wrong. The maps and their mutex move onto a
`stubRegistry` value with an `ensureOn` method; the two exported starters ask the registry and fail the test on its
error. The write now lives on a receiver, where it belongs, and the test-facing functions hold no package state.

D69 — **The log-kind scan resolves a receiver by declaration, and loggerness is a declared method shape.** The
alternative was to keep the name set and subtract the names that misfire, which is the same defect with a
blocklist: the set is tree-wide, so any future `t := now` in any package re-enters it. The rule built instead is
local and refutable — a receiver's type comes from the binding in scope, and a type is a logger when it declares a
method the emit-site set names with a message `string` at the message index and a variadic tail. It admits the
tree's one-method logger interfaces and refuses `*testing.T` and `error` on their declarations rather than on
their names. `*slog.Logger` and its `New`/`Default`/`With`/`WithGroup` constructors stay a hardcoded standard-
library fact, because the scan parses only this tree.

D70 — **The context-deadline detector reads every `context.WithTimeout` and `WithDeadline` in test code**,
superseding D65. D65 fired only where the enclosing function also named `DeadlineExceeded`, which no real test file
does, so the detector read zero sites and the three real ones stood. The qualifier "whose expiry feeds a verdict"
is what the **class marker** decides, not what the pattern decides: a deadline whose expiry is not a verdict input
carries `pacing` or `outcome` with a justification, exactly as a sleep does, and one whose expiry is a verdict
input has no admitting class and must be converted. That is the same shape every other construct the lint reads
already has, and it leaves nothing to a reading audit.

D71 — **The package-state check reaches a write through a call**, superseding D66. D66 read only syntactic writes,
so a test that handed its value to a setter wrote package state and the lint said nothing — the laundering the
finding named. The check now follows the value: a receiver-less function that assigns a package variable from a
parameter is a writer, a function that passes its own parameter to a writer is a writer, and a test's call to
either is the test's write. D66's concern — memoised harness singletons and other shared *resources* — is answered
by the same rule rather than by an exemption: those functions memoise a value they compute themselves, take
nothing from the caller into the variable, and are not writers.

F4 — **Ruled: "through a call" reaches the value the test hands in; reading (b) stands.** The architect
overturned the fork. `decision:test-wallclock-lint-ratchet` bans the construct because a test can hide a verdict in
shared state, and the state a test hides a verdict in is the state the test installs. Reading (a) fails any test
that merely runs the product. Of its 141 sites, 102 are a test calling a CLI verb, a role launcher, or a harness
that memoises a value it computes itself. The decision admits no wait class over a package-state write, so the
lint's only remedy at those sites is to redesign correct product singletons — a product change the decision does
not ask for, imposed by a lint over test code. Reading (b) catches every site the finding is about, its own
examples included, and the sprint swept all 39. No reasonable owner adopts a check that fails a test for running
the product. The built reading stands. The architect changed no file and filed no issue.

D72 — **The two already-applied migrations keep their pre-rename comment text.** The vocabulary sweep rewrote
`host-agent` to `host-daemon` inside `001-initial.sql` and `038-instance-target-routing-identity.sql` on both
backends. `Migrator.Run` hashes each applied file and refuses to boot when the hash changes, so the sweep fails
every existing database at its next boot, and `decision:migrations-append-only-numbered` makes an applied file
immutable. I restored the committed bytes of all four files. Migration 038 therefore still cites
`@concept: host-agent-proxy`, a slug the corpus renamed to `host-daemon-proxy`. The migration stays immutable and
the rename does not reach it: the lint reads no `.sql` file, so the line resolves nowhere and fails nothing, and
the concept the comment describes is the same concept. The same reasoning leaves `releases/*.md` — the shipped
release notes — on the old vocabulary.

D73 — **The package-state check reads a helper that takes no arguments as writing for its caller**, extending
D71. D71 bound every writer to a parameter, so `func reset() { pool = nil }` laundered a write that the same
statement inside the test failed on. A receiver-less function taking no arguments does nothing for its caller but
write, so its call is the caller's write whatever value it assigns; `writesOnItsCallersBehalf` states that
condition. The fixpoint through calls stays parameter-bound, so the fix leaves F4's ruling in place: a role
launcher, a CLI verb, and a memoised harness singleton all take arguments and all install a value they compute
themselves, so none of them becomes a writer. I measured both readings over the tree. Widening every function
whatever its arity surfaces 16 sites, all of them a test starting a role and reaching
`processIdentity.refCount = 1` — the reading F4 overturned. The arity-bound widening surfaces none, and the
baseline stays empty. `TestATestsCallToAResetHelperTakingNothingIsTheTestsWrite` holds the new reading.

D74 — **The second outbox row goes in after the attempt, and the existing due-time assertion carries the new
proof.** `testLifecycleOutboxCarriesItsDeliveryFailureState` reads its head through a helper that requires the
scope to hold exactly one row, so a second row staged up front breaks every earlier assertion. I stage it after
the attempt-count assertions and before the due-time reads. The existing
`ListOldestPendingPerStream(…, nextAttempt-1s, …)` assertion then proves what no assertion proved before: the
stream returns nothing while its head waits, rather than the row behind it.

D75 — **A caller that takes no arguments inherits every writer it calls**, completing D73. D73 made
`func reset() { pool = nil }` a writer. The propagation guard still demanded that a caller pass one of its own
parameters, so `func resetForTest() { reset() }` inherited nothing and a test calling the wrapper passed. The
guard now asks for a parameter only where the caller has one: `len(params) > 0 && !anyArgumentIsAParameter(...)`.
A caller taking no arguments does nothing for its own caller but make that call, so it carries the callee's
writes. The parameterised case keeps F4's reading unchanged. The lint over the tree surfaces no new site and
the baseline stays empty. `TestAWrapperTakingNothingAroundAResetHelperLaundersNothing` holds the new reading
beside the two-helper case D71 already covered.

D76 — **The drain's grace-window test waits on a SIGTERM receipt the child writes, and checks that `Drain` has
not returned.** The old wait asked whether the child was still alive. That held from spawn, so the test returned
on its first poll and proved nothing about the SIGTERM or the window. The fixture binary now writes the file named
by `env:RIMSKY_TEST_SIGTERM_RECEIPT` when it receives SIGTERM. The test waits for that file, asserts the child
outlived the signal, and then reads `drained` without blocking: `Drain` holds every straggler for the whole grace
window, so a return before `close(graceOver)` fails the test. I took both remedies the finding offered rather
than one, because each covers a different half — the receipt proves the SIGTERM landed, the non-blocking read
proves the window was honoured. I checked the proof against the regression it names. With the grace channel
closed from the start, `Drain` SIGKILLs the child before it can write the receipt. The wait then never ends and
the run's watchdog stops the run. The old test passed that same mutation.

D77 — **`Opts.McpModules` is the one way to declare an MCP module; the package-global registry is gone.**
`RegisterMcpModule`, `lookupMcpModule`, the registry map and its mutex, and the `resolveMcpModule` fallback are
deleted. The one caller, the fake-CLI scenario server, now builds a `map[string]claudeagent.ModuleMcpFactory` and
assigns it to `opts.McpModules` after `LoadOptsFromEnv`. With the fallback gone, `resolveMcpModule` was a
one-line map read at two sites, so I inlined it rather than keep a hop. Both error strings now name
`Opts.McpModules`: "is not a declared MCP module (declare it in Opts.McpModules)".

D78 — **The lifecycle e2e test is deleted and its outbox proof moves into the canary.** The reviewer offered
either giving the stub a call recorder or deleting in favour of the canary. `TestCanary_LifecycleSubscriberCallbackContract`
already drives the identical sequence — deploy, create, terminate, delete, undeploy, deregister. It waits for
each callback at a fake subscriber and asserts each verb arrived exactly once, so it proves everything the
deleted test claimed by name. The deleted test held one proof the canary lacked: after each transition the
scope's outbox drains. I moved that proof across as `requireOutboxDrains`, called after each transition, and it
now waits for the drain rather than reading once — the old read ran immediately after the request returned, so it
could not tell an acknowledged delivery from a row not yet staged. Threading a recorder out of the shared stub
fixture instead would have changed `testfixture.Start`'s signature at 71 call sites to serve one test.

D79 — **The reader-lease classification is gated on the reader's own bound having fired.**
`readerOpenOutlastedItsBound` treated any `Canceled` as the forbidden pattern, so a server-side cancellation
made an honest producer fail the check. Each reader now records its bound context's `Err()` at the moment `Open` returns,
before the deferred cancel runs, and the classifier requires that error to be present and to match what `Open`
returned. The gRPC mapping stays: a deadline bound admits `codes.DeadlineExceeded`, a cancel bound admits
`codes.Canceled`. A failure arriving while the bound is still live now falls to the inconclusive-probe branch.
`TestCheckSerialization9b_ReportsAServerCancellationAsAnInconclusiveProbe` holds the distinction.

D80 — **I deleted the stub lifecycle package; `test/support` uses the shared subscriber.** Its `Call` type,
`Calls()`, `record`, and the mutex and slice behind them had no reader: `server.RunWithStore` builds the server as
a local and never hands it back. The canary test already carries the recording idiom in its own fake subscriber,
so a second recorder had no work to do. Deleting the recording left the package byte-identical to
`lib/services/claim_producers/shared/lifecycle`, so the package itself was the duplicate. I deleted it, and
`test/support/claim_producers/stub/server` imports the shared package.

D81 — **`OnRunScopeTerminal` is implemented once, and one table test guards all seven.** The stub answered six
RPCs and embedded the `Unimplemented` base, so a run-scope terminal routed at it would retry on backoff forever
and read as a stall. The shared server already implements all seven, so importing it closes the gap at the one
implementation. Its `TestServer_AllSevenRPCsImplemented` is the guard: a table that counts its rows and rejects an
`Unimplemented` code. The `var _ genv1.LifecycleSubscriberServer` assertion sits beside it but cannot carry the
check, because the embedded base satisfies the interface. The guard now carries the sharper failure message,
which names the stall a missing RPC would cause. I checked it against the gap it closes: with
`OnRunScopeTerminal` removed the test fails naming the RPC and the stall.

D82 — **I severed a dependency that was never a boundary, and restored it.** My first pass kept the stub package
beside the shared one and justified it as a module boundary the project draws on purpose. That reading was wrong
on the facts. The root `go.mod` requires `lib/services` with a replace, `go.work` joins all four modules, and
`cmd/internal/bundledwire` already imports `lib/services/bundled`. The `consumption-side-isolation` depguard rule
constrains what `lib/services` imports, not what imports it — its own comment says so. Nothing forbade the
import. Keeping two live byte-identical copies, with neither the import nor a rule between them, was worse than
either option the reviewer named.

## Certification ledger

| id | site | producer | round entered | outcome | repeats | rounds touched | note |
|---|---|---|---|---|---|---|---|
| C1 | report F1 — delete route and `instance_terminated` | alignment | 1 | fixed 1 | 0 | 0 | architect OVERTURNED; built reading (b) stands, no edit |
| C2 | report F2 — recovery edge reading | alignment | 1 | fixed 1 | 0 | 0 | architect OVERTURNED; built reading (b) stands, no edit |
| C3 | report F3 — service pending-delivery surface | alignment | 1 | fixed 1 | 1 | 1 | architect OVERTURNED and fixed: executor route reports `pending_lifecycle_deliveries`; reviewer 3 repeated the fork, subtracted |
| C4 | report F4 — package-state reach depth | alignment | 1 | fixed 1 | 0 | 0 | architect OVERTURNED; built reading (b) stands, no edit |
| C5 | migrations 001/038 both backends — comment text edited after apply | code review (its C1) | 1 | fixed 1 | 0 | 1 | committed bytes restored (D72) |
| C6 | `handler_transaction_wrap_test.go:76` — executor route absent from fitness list | code review (its C2) | 1 | fixed 1 | 0 | 1 | route added to the paths slice |
| C7 | `scheduler.go:129` — dead `h *Handle` parameter on `tick` | code review (its C3) | 1 | fixed 1 | 0 | 1 | parameter dropped at ten call sites |
| C8 | `conformance/lifecycle_outbox.go:341` — doubled time comparison | code review (its C4) | 1 | fixed 1 | 0 | 1 | one comparison; stray blank line removed |
| C9 | `packagestate.go:171` — zero-parameter writer escapes the detector | code review (its C5) | 1 | fixed 1 | 0 | 1 | arity-bound widening (D73); baseline stays empty |
| C10 | outbox conformance — no proof a backed-off head withholds its stream's next row | code review (its C6) | 1 | fixed 1 | 0 | 1 | second row staged after the attempt (D74) |
| C11 | `packagestate.go:190` — zero-argument wrapper launders a write | code review 2 (its C7) | 2 | fixed 2 | 0 | 1 | parameterless caller inherits the write (D75) |
| C12 | `compose/shutdown_test.go:277` — grace-window test's wait observes nothing | code review 2 (its C8) | 3 | fixed 3 | 0 | 1 | SIGTERM receipt file + Drain-not-returned assertion (D76) |
| C13 | `moduleloopback.go` — two live ways to register an MCP module | code review 3 (its C9) | 4 | fixed 4 | 0 | 1 | global registry deleted; `Opts.McpModules` is the one idiom (D77) |
| C14 | `lifecycle_e2e_test.go` — full-sequence test passes when nothing stages | code review 3 (its C10) | 4 | fixed 4 | 0 | 1 | stub records calls; transitions asserted (D78) |
| C15 | `serialization9b.go:130` — plain Canceled misclassified as reader-lease | code review 3 (its C11) | 4 | fixed 4 | 0 | 1 | classification bound to the probe context (D79) |
| C16 | three failure messages name retired kinds | code review 3 (its C12) | 4 | fixed 4 | 0 | 1 | messages name the new kinds |
| C17 | `runner_subclaim.go:286,308` — event kinds duplicated as raw literals | code review 3 (its C13) | 4 | fixed 4 | 0 | 1 | constructors `.String()` passed |
| C18 | demo script prints literal `%s` | code review 3 (its C14) | 4 | fixed 4 | 0 | 1 | echo corrected |
| C19 | stub lifecycle recorder unreachable | code review 3 (its C15) | 5 | fixed 5 | 0 | 1 | D80 |
| C20 | stub lifecycle server lacks `OnRunScopeTerminal` | code review 3 (its C16) | 5 | fixed 5 | 0 | 1 | seventh RPC + guard (D81) |
| C21 | stub lifecycle package duplicates `shared/lifecycle` on a boundary that does not exist | code review 3 (its C17) | 6 | fixed 6 | 0 | 1 | duplicate deleted; import restored (D82) |

## Certification — Sprint: Attribute bytes in the row, lifecycle delivery through one outbox, log kinds in the standard's form

Status: certified clean

### Outcomes delivered

- `decision:attribute-bytes-in-the-row`, `decision:scratch-column` — an attribute bag and a node-run's scratch commit whole in a byte column of their own row; the blob backend, its spill, its orphan ledger, and its sweep are gone, and an over-cap write fails naming the run, the value, and the byte count.
- `decision:lifecycle-fanout-after-commit`, `decision:lifecycle-subscriber-at-least-once-delivery`, `decision:lifecycle-drain-per-role` — every lifecycle event is staged in the transaction that performs its transition and drained by a per-role reconciler with a kick and a due time; the idempotency ledger is gone; a deleted instance's undelivered events still deliver.
- `decision:service-delivery-stall-signal` — both service outboxes persist their failure state on the row; a stall writes one event-log entry and the recovery writes one; `GET /v1/admin/diagnostics/lifecycle-outbox` (under `diagnostics:read`) and both service-status routes report what a service is owed; `cfg:service_delivery.stall_after` caps the backoff, and the config loader refuses a retention window the stall signal could not beat.
- `decision:structured-log-kind-format` — every process-log kind in the tree takes `SUBSYSTEM.NOUN.VERB` (588 literals across 142 files), and `tools/logkind-lint` gates the form in `make lint` with an empty baseline.
- `decision:test-wallclock-lint-ratchet` — the fifty-five off-standard tests are remediated, and the wall-clock lint reads context deadlines and package-state writes (through calls) with an empty baseline.
- The vocabulary sweep — "peer" is "service" and the host agent is the host daemon across code, protos, config, tests, images, CLI, env vars, and documents; 21 corpus artifacts renamed, the proxy still supervisor-blind.
- `story:single-process-all-in-one` and the eleven renamed host-daemon/service stories hold under their new slugs with their tests re-annotated.

### Divergences

The build recorded D1–D71 and claimed forks F1–F4; the gate's fixer and architect added D72–D82 and ruled every fork. The architect's rulings: F1 (delete route stages no second `instance_terminated`) OVERTURNED, built reading stands; F2 (recovery is the negated stall predicate) OVERTURNED, built reading stands; F3 (service pending-delivery surface) OVERTURNED with a fix — the executor observability route now reports `pending_lifecycle_deliveries` beside the claim-producer route; F4 (package-state reach is parameter-bound) OVERTURNED, built reading stands. No dissolution, no reversal; one refutation inside the build (F1's option (a), refuted by the canary contract test). Corpus repairs during the gate: none — no `.ok-planner/design/` file changed after the deltas landed. Each entry below keeps its identifier; every one is open to after-the-fact veto.

#### The record, one line per entry

- D1 — The engine cap is checked in rimsky's writer, and the constant is the smaller engine's true cap.
- D2 — `MergeAttributeBag` is shared between the two drivers. Postgres merged an attribute delta with the
- D3 — The run directory no longer holds `blobs/`. The amended `decision:artifact-layout` names only the run's
- D4 — `RunScheduler` / `RunSupervisor` / `RunControlAPI` lost their pre-opened-backend parameter. With no
- D5 — Two scheduler tests changed instrument. `TestScheduler_Tick_NilClockDoesNotPanicAndStillSweepsOrphanBlobs`
- D6 — The all-in-one story test changed what it drives. `story:single-process-all-in-one` no longer promises
- D7 — `README.md` was edited alongside `docs/`. The work item named `CLAUDE.md` and `docs/`. The README's
- D8 — The design-corpus TOCs were hand-edited. `.ok-planner/design/concepts.md` and `decisions.md` say they
- D9 — `TransientParkSignalPayload.scratch_spilled` was retired rather than left set to `false`. The project's
- D10 — `tidy` walks all four workspace modules, and `tidy-docker` with it. The retired
- D11 — `docs/config.md`'s driver-comparison paragraph was rewritten, not just trimmed. The work item said to
- D12 — Only the staged-outbox drain moved to `lib/runtime`; the terminated-instance pass stayed behind. The
- D13 — The stall-signal decision is not applied yet, so this stage's code cites at-least-once delivery. The
- D14 — The event enum gains `EventRunScopeTerminal` and the drain delivers run-scope rows now. The work item
- D15 — `PassesCompleted()` on the drain. A test that proves the kick wakes the drain has to know the drain
- D16 — The retry backoff is shared with the producer-verb dispatcher. Both outboxes widen a retry the same
- D17 — A staged row's due time comes from the wall clock that stamped it, not from an injected clock.
- D18 — `docs/config.md` gained a `service_delivery` section. The work item named the configuration key, not
- D19 — The SQLite migration backfills `next_attempt_at` with the epoch. SQLite's `ALTER TABLE ADD COLUMN`
- D20 — A role process that owns its drain still dials a lifecycle subscriber registry. The work item says "the
- D21 — The scope-kind enum is renamed off the deleted ledger. `LifecycleIdempotencyScopeKind` and its three
- D22 — The delivery re-reads its row inside the advisory lock. The ledger's read-inside-the-lock was what made
- D23 — A frame settlement stages a terminal only for the scopes it closes. `closeSettledFrameScopeTree`
- D24 — The claim-producer observability route reports pending outbox rows. `GET /v1/observability/claim-producers/{name}`
- D25 — Stage 3's new outbox surfaces cite `concept:lifecycle-subscriber`, not the stall-signal decision.
- D26 — Five documents under `docs/` were corrected. No Stage 3 work item names `docs/`, and the record
- F1 — Ruled: the delete route stages no second `instance_terminated`; reading (b) stands. The architect
- D27 — Six tests that read the attribute bag through raw SQL were repaired. Running the whole
- D28 — No migration for the producer-verb outbox's failure state. The work item says "the producer-verb outbox
- D29 — The stall edge is a persisted marker, not a remembered set. The work item offers either. A remembered
- D30 — The config loader refuses a retention window that is not longer than the stall threshold. The amended
- D31 — Five documents under `docs/` gained the new surface. No Stage 4 work item names `docs/`. Two of the
- D32 — Only the read paths that serve a route are bounded. The reviewer left Stage 4 to decide whether to bound
- D33 — The two new process-log kinds take the standard's form; the pre-existing ones nearby do not. Stage 6
- F2 — Ruled: a stalled service recovers when nothing it owes is past the threshold; reading (b) stands. The
- D34 — `lib/runtime/peer` became `lib/runtime/service`. The sprint names one package by hand,
- F3 — Ruled: the pending-delivery field belongs on the service-status route of every service kind; reading (b)
- D36 — The sprint's `### Amend concept: host-agent-proxy` heading points at a sidecar file that does not
- D37 — The design TOCs were hand-edited again. D8 records why. This stage rewrote every touched summary in
- D38 — "Agent" survives in more places than the sprint's two-clause exemption names. The sprint exempts the
- D39 — `lib/services/internal/agentport` became `daemonport`. The sprint names `RIMSKY_AGENT_PORT` and the
- D40 — Stale port numbers in `docs/` were corrected. The sweep rewrote the sentences naming the proxy's two
- D41 — `releases/*.md` were left alone. They describe what shipped at a past version, in the vocabulary that
- D42 — An example's service name was chosen rather than swept. `docs/examples/audit-artifact.md` named its
- D43 — The fixed TLS server name changed. `enroll.PeerServerName = "peer.rimsky.internal"` is
- D44 — Two operator-facing names outside the work item's enumeration moved. The work item enumerates the
- D45 — The proto rename changes a gRPC full method name, and that is this sprint's largest outward break.
- D46 — `docs/env-vars.md` carried four rows no code backs. It also carried one false claim about the
- D47 — The separate supervisor configuration file the documents describe does not exist, and every site that
- D48 — The subsystem vocabulary is one first segment per bounded area of the tree, not per Go package. The
- D49 — Prose is preserved where the kind cannot carry it, and dropped where it can. The work item says a
- D50 — A Go symbol in a literal became a `site` field, and that field is what lets one kind serve several call
- D51 — A message the lint cannot read statically is a violation, and a kind a caller already named is checked at
- D52 — `Log` and `LogAttrs` are read, and the tree emits neither. The scan takes the message from argument
- D53 — `make lint` runs the lint through a `-check` mode; the fitness test is the suite's gate. The work item
- D54 — The shared logger's own unit test carries kind-form fixtures. `lib/foundation/shared/logger_test.go`
- D55 — Two test assertions matched a substring of the old message rather than the whole literal.
- D56 — Six `docs/` sentences named a process-log message that no longer exists. No Stage 6 work item names
- D57 — The lint scans `test/` as well. `decision:structured-log-kind-format` says the lint runs "over the
- D58 — One ledger row dissolved before this stage reached it. The ledger calls
- D59 — The claim-producer conformance check takes its reader-open bound as a parameter, not a `Clock`. The
- D60 — `RunnerOpts` gains a `Scenarios` field rather than a test-only registry seam. The flagged test appended
- D61 — The demo script's two deadline polls become unbounded polls, and a third boot bound goes. The ledger
- D62 — The claude-agent loop takes an injected `now` and ticker, not `shared.Clock`. The sprint says to inject
- D63 — The daemon-stop escalation takes a process-control value, not just a clock. Injecting a clock alone
- D64 — `shared.AutoAdvanceClock` joins `ControllableClock`. A deadline-bounded product loop driven by
- D65 — The context-deadline detector fires only where the enclosing function names deadline expiry. The work
- D66 — The package-state check reads writes from test functions and `testing.TB` helpers, not every write in a
- D67 — The claude-agent MCP module registry becomes injectable, and `ParseCliConfig` takes the map. The flagged
- D68 — The services harness's two stub memos move onto a registry type. `stubLaunched` and `stubErrLaunched`
- D69 — The log-kind scan resolves a receiver by declaration, and loggerness is a declared method shape. The
- D70 — The context-deadline detector reads every `context.WithTimeout` and `WithDeadline` in test code,
- D71 — The package-state check reaches a write through a call, superseding D66. D66 read only syntactic writes,
- F4 — Ruled: "through a call" reaches the value the test hands in; reading (b) stands. The architect
- D72 — The two already-applied migrations keep their pre-rename comment text. The vocabulary sweep rewrote
- D73 — The package-state check reads a helper that takes no arguments as writing for its caller, extending
- D74 — The second outbox row goes in after the attempt, and the existing due-time assertion carries the new
- D75 — A caller that takes no arguments inherits every writer it calls, completing D73. D73 made
- D76 — The drain's grace-window test waits on a SIGTERM receipt the child writes, and checks that `Drain` has
- D77 — `Opts.McpModules` is the one way to declare an MCP module; the package-global registry is gone.
- D78 — The lifecycle e2e test is deleted and its outbox proof moves into the canary. The reviewer offered
- D79 — The reader-lease classification is gated on the reader's own bound having fired.
- D80 — I deleted the stub lifecycle package; `test/support` uses the shared subscriber. Its `Call` type,
- D81 — `OnRunScopeTerminal` is implemented once, and one table test guards all seven. The stub answered six
- D82 — I severed a dependency that was never a boundary, and restored it. My first pass kept the stub package

### Findings fixed

- Sprint alignment (the corpus-change judge): clean on deltas, work items, and coherence in every round; its four claimed-fork findings were ruled by the architect (three no-change, one fixed).
- The mechanical floor (annotation integrity, plumbline lint, catalog TOCs, ok-workspaces discipline sweep): clean on first pass and every round after.
- Test suites: `make test-all` (all four modules, docker-backed scenario and services suites included) passed mid-run and again, end to end, on the final tree. One earlier run of the final tree failed at the root-module target and kept only a 30-line log tail, so the failing test's name is lost; two verbatim re-runs of that target and the full green `make test-all` followed on the identical tree. The anomaly is recorded here for the owner: the project's no-flakes rule says a failure that does not reproduce is still a fact worth a name, and this one has none.
- Code review (three standing reviewers over 1,038 changed files): 17 findings, all fixed and verified — among them the edited applied migrations (C5), the executor route missing from the transaction fitness list (C6), two wall-clock-lint blind spots (C9, C11/C19 numbering per ledger), a vacuous grace-window test (C12), a lifecycle e2e test that proved too little (C14), and a duplicated lifecycle stub package (C21).
- The loop subtracted 1 repeat (the route fork, already ruled) and ruled 0 reversals.

### The finding ledger

The `## Certification ledger` section above holds the table: 21 rows, all settled — 4 architect-ruled forks, 17 fixed findings across 6 fixer batches.

### Dissolved

None.

### Issues promoted

None — every fork was overturned by the architect and no remainder reached the cap, so the intake gained nothing and `/verify-issues` was skipped.

The sprint certified clean. The standing offer: archive the sprint — move it with its completion report, its ledger file, its delta sidecar, and the six promoted issue files to `.ok-planner/history/` — and commit the work. Both are your acts; say the word and I do both, then stamp the archived sprint with the closing commit.
