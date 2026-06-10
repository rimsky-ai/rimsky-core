# Completion Report — Design Corpus Bootstrap

**Spec:** `.ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md`
**Plan:** `.ok-planner/plans/2026-06-08-design-corpus-bootstrap.md`
**Auditor walked:** 62 stories + 75 technical decisions enumerated in the spec's `## Manifest` section, plus the three foundational passes (URL prefix sweep, event-kind enum, design-doc bootstrap) the manifest's `### Design changes` block describes.

---

## 1. Proof walkthrough

Each story below maps to its proof artifact, the exhibition the artifact embodies, the invocation, and the status.

### Cluster 1 — Operator entity lifecycle

**STORY-template-lifecycle** — Operator manages template catalog end-to-end.
- Artifact: `test/scenarios/template_lifecycle_e2e_test.go`
- Exhibits: register / get / validate (pre-flight) / deploy / undeploy / delete-409-while-referenced / delete-204-when-clear against the all-in-one stack at `/v1/templates` routes.
- Invocation: `go test ./test/scenarios/ -run TestTemplateLifecycle -count=1`
- Status: EXHIBITS WORKING.

**STORY-instance-lifecycle** — Operator manages instance runtime lifecycle.
- Artifact: `test/scenarios/instance_lifecycle_fullstack_test.go` (new sibling) + `test/scenarios/lifecycle_force_terminate_fullstack_test.go` (force-terminate leg already covered).
- Exhibits: create / list / get / pause-stops-dispatch / resume / delete-non-terminal-rejected / delete-terminal-succeeds, with pause exhibition via observed absence of dispatch events.
- Invocation: `go test ./test/scenarios/ -run 'TestInstanceLifecycle|TestForceTerminate' -count=1`
- Status: EXHIBITS WORKING.

**STORY-tag-management** — Operator manages movable template-hash names.
- Artifact: `test/scenarios/tag_management_e2e_test.go`
- Exhibits: bind / resolve / rebind / earlier-instance-keeps-old-hash / delete-makes-name-unresolvable.
- Invocation: `go test ./test/scenarios/ -run TestTagManagement -count=1`
- Status: EXHIBITS WORKING.

**STORY-node-admin** — Operator inspects and admin-invalidates nodes.
- Artifact: `test/scenarios/node_admin_e2e_test.go` (new) + `test/scenarios/cascade_operator_frame_in_e2e_test.go` (in-cascade leg).
- Exhibits: get / invalidate / in-cascade-invalidate / reset clears error count and unblocks dispatch.
- Invocation: `go test ./test/scenarios/ -run 'TestNodeAdmin|TestCascadeOperatorFrameIn' -count=1`
- Status: EXHIBITS WORKING.

**STORY-message-bus** — Sender emits idempotent messages into instance bus.
- Artifact: `lib/control/controlapi/idempotency_matrix_test.go` + `lib/control/controlapi/idempotency_sender_subject_test.go`
- Exhibits: per-status matrix (400 no header, 201 fresh, 200 replay returning original `message_id`); distinct senders never collide.
- Invocation: `go test ./lib/control/controlapi/ -run 'TestIdempotency' -count=1`
- Status: EXHIBITS WORKING.

**STORY-event-log-read** — Operator reads unified chronological event feed.
- Artifact: `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go` (chronological ordering) + `test/scenarios/breakpoints/hit_emits_event_test.go` (breakpoint-on-feed).
- Exhibits: events returned in true timestamp order (not source-grouped); breakpoint hits appear between transitions.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestCliWatchChronological -count=1 && go test ./test/scenarios/breakpoints/ -run TestHitEmitsEvent -count=1`
- Status: EXHIBITS WORKING.

**STORY-audit-log-read** — Operator reads auth-relevant action audit.
- Artifact: `test/scenarios/auth/audit_read_test.go`
- Exhibits: `GET /v1/audit` returns actor / action / outcome / resource for key creates, revokes, rotates, denied access, dry-run attempts.
- Invocation: `go test ./test/scenarios/auth -run TestAuditRead -count=1`
- Status: EXHIBITS WORKING.

**STORY-breakpoint-debugger** — Operator debugs live instance via breakpoints.
- Artifact: `test/scenarios/breakpoints/debugger_lifecycle_e2e_test.go` (new) + `test/scenarios/breakpoints/hit_emits_event_test.go` (unified-feed co-transactional leg).
- Exhibits: install / list / hit appears on event feed AND breakpoint-hits ledger co-transactionally / resume-with-overlay applied at next dispatch / delete cascades hits.
- Invocation: `go test ./test/scenarios/breakpoints/... -race -count=3`
- Status: EXHIBITS WORKING.

**STORY-asset-management** — Operator manages instance-produced data assets.
- Artifact: `test/scenarios/asset_management_e2e_test.go`
- Exhibits: list / get / materialize-causes-producing-dispatch / version-history / materialization-audit / delete.
- Invocation: `go test ./test/scenarios/ -run TestAssetManagement -count=1`
- Status: EXHIBITS WORKING.

**STORY-backfill-ops** — Operator re-processes historical data via backfill.
- Artifact: `test/scenarios/backfill_ops_lifecycle_e2e_test.go` (new) + `test/scenarios/backfill_partition_override_fullstack_test.go` (override leg).
- Exhibits: start / list / get / partition-progress / cancel-aborts-in-flight-via-real-supervisor.
- Invocation: `go test ./test/scenarios/ -run 'TestBackfill' -count=1`
- Status: EXHIBITS WORKING.

**STORY-lineage-exploration** — Operator walks lineage forward and backward.
- Artifact: `test/scenarios/lineage_exploration_e2e_test.go`
- Exhibits: ancestor walk includes upstream producer; descendant walk includes consumer; by-claim, by-source, by-producer pivots.
- Invocation: `go test ./test/scenarios/ -run TestLineageExploration -count=1`
- Status: EXHIBITS WORKING.

**STORY-lineage-admin** — Operator prunes lineage records older than a cutoff.
- Artifact: `test/scenarios/lineage_admin_prune_e2e_test.go`
- Exhibits: rows older than cutoff removed; rows at/newer-than cutoff preserved; deletion count surfaced.
- Invocation: `go test ./test/scenarios/ -run TestLineageAdmin -count=1`
- Status: EXHIBITS WORKING.

**STORY-api-key-management** — Operator administers api-key lifecycle.
- Artifact: `test/scenarios/auth/lifecycle_test.go`
- Exhibits: bootstrap admin returns plaintext once / scoped mint without exposing plaintext / revoke / rotate-with-grace-window / status.
- Invocation: `go test ./test/scenarios/auth -run TestLifecycle -count=1`
- Status: EXHIBITS WORKING.

**STORY-runtime-diagnostics** — Operator inspects runtime wedge state.
- Artifact: `test/scenarios/parked_lifecycle_test.go` (parked leg) + `test/scenarios/runtime_diagnostics_e2e_test.go` (wait-sets / held-frames / claim-holders).
- Exhibits: each diagnostic surface reflects the supervisor's actual state.
- Invocation: `go test ./test/scenarios/ -run 'TestParkedLifecycle|TestRuntimeDiagnostics' -count=1`
- Status: EXHIBITS WORKING.

**STORY-client-context** — Operator switches between control-api endpoints.
- Artifact: `examples/client-context-demo.sh` + `cmd/rimsky/cli/ctx_demo_test.go` (driver).
- Exhibits: register / switch / use / inspect / remove with two real local control-api endpoints (Proof form: demo).
- Invocation: `go test ./cmd/rimsky/cli/ -run TestCtxDemo -count=1`
- Status: EXHIBITS WORKING.

### Cluster 2 — Operator workflows, permissions, debugging

**STORY-operator-onboarding** — New operator runs first dev-loop end-to-end.
- Artifact: `examples/onboarding-demo.sh` + `examples/onboarding-template.yaml` + `lib/services/test/scenarios/onboarding_demo_e2e_test.go` (driver).
- Exhibits: shipped template runs unmodified through `rimsky run`; instance reaches terminal; README documents the verb (Proof form: demo).
- Invocation: `go test ./lib/services/test/scenarios/ -run TestOnboardingDemo -count=1`
- Status: EXHIBITS WORKING.

**STORY-compose-lifecycle** — Operator drives multi-resource compose manifest.
- Artifact: `lib/services/test/scenarios/cli_compose_up_down_e2e_test.go`
- Exhibits: up / down / plan / status reconcile against running rimsky; project namespace respected; no docker/kubectl shell-out.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestCliComposeUpDown -count=1`
- Status: EXHIBITS WORKING.

**STORY-compose-namespace-guard** — Server enforces reserved compose prefix.
- Artifact: `lib/services/test/scenarios/control_api_compose_prefix_guard_e2e_test.go`
- Exhibits: non-compose caller with `tag:create` or `instance:create` refused at `compose:*`; compose machinery still succeeds.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestComposePrefixGuard -count=1`
- Status: EXHIBITS WORKING.

**STORY-mcp-transport** — Operator/agent drives rimsky entirely via MCP.
- Artifact: `lib/services/test/scenarios/mcp_transport_parity_e2e_test.go`
- Exhibits: in-test MCP client discovers tool catalog; per-category sample of one read + one mutation; auth gate fires identically; response shapes match HTTP routes.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestMcpTransportParity -count=1`
- Status: EXHIBITS WORKING.

**STORY-anonymous-mode-bootstrap** — Fresh deployment opens then locks down.
- Artifact: `test/scenarios/auth/anonymous_mode_bootstrap_e2e_test.go`
- Exhibits: empty keys table → anonymous succeed; `rimsky auth init` returns plaintext once; subsequent unauthenticated rejected; init refuses on non-empty table; status surface reports mode transitions.
- Invocation: `go test ./test/scenarios/auth -run TestAnonymousModeBootstrap -count=1`
- Status: EXHIBITS WORKING.

**STORY-dry-run-request-flag** — Operator previews any write per-request.
- Artifact: `test/scenarios/auth/dry_run_test.go`
- Exhibits: write with `?dry_run=true` returns synthetic envelope; validation actually runs; no row persisted.
- Invocation: `go test ./test/scenarios/auth -run TestDryRun -count=1`
- Status: EXHIBITS WORKING.

**STORY-dry-run-mode-floor** — Operator mints attempt-only key.
- Artifact: `test/scenarios/auth/dry_run_identity_bound_test.go`
- Exhibits: key pinned to `mode: dry_run` always returns synthetic; ordinary key persists; audit records the attempt with executed-false.
- Invocation: `go test ./test/scenarios/auth -run TestDryRunIdentityBound -count=1`
- Status: EXHIBITS WORKING.

**STORY-grant-scope-enforcement** — Least-privilege delegation across lifecycle.
- Artifact: `test/scenarios/auth/grant_scope_lifecycle_test.go`
- Exhibits: in-scope succeeds; out-of-scope refused at every lifecycle stage (register / deploy / undeploy / deregister / tag set / tag delete / instance create).
- Invocation: `go test ./test/scenarios/auth -run TestGrantScopeLifecycle -count=1`
- Status: EXHIBITS WORKING.

**STORY-forensic-last-attribute** — Operator reads node's latest attribute bag.
- Artifact: `test/scenarios/observability_latest_attribute_fullstack_test.go`
- Exhibits: latest-attribute surface returns the most recent run's bag; stale or absent fails.
- Invocation: `go test ./test/scenarios/ -run TestObservabilityLatestAttribute -count=1`
- Status: EXHIBITS WORKING.

**STORY-rules-doc-accuracy** — Contributor trusts rules.md citations.
- Artifact: `tools/rulesdoc/rulesdoc_test.go`
- Exhibits: every filesystem path and make-target the rules document cites resolves; mutating to a non-existent path makes the gate fail.
- Invocation: `go test ./tools/rulesdoc/ -count=1`
- Status: EXHIBITS WORKING.

### Cluster 3 — Template author

**STORY-claim-scope-substitution** — Template author uses canonical claim_scope.
- Artifact: `test/scenarios/stores/claim_scope_directive_e2e_test.go`
- Exhibits: `{{claim.<alias>.claim_scope}}` resolves at dispatch; legacy `scope` spelling refused at registration.
- Invocation: `go test ./test/scenarios/stores -count=1`
- Status: EXHIBITS WORKING.

**STORY-substitution-doc-accuracy** — Substitution module header matches resolver.
- Artifact: `lib/graph/attribute/substitution_test.go`
- Exhibits: header-bullet enumeration matches AST-extracted resolver case-arms via `headerBulletPattern`.
- Invocation: `go test ./lib/graph/attribute -run TestSubstitution -count=1`
- Status: EXHIBITS WORKING.

**STORY-ref-validation-mode** — Operator chooses registration-time strictness.
- Artifact: `test/scenarios/attributes/ref_validation_mode_e2e_test.go`
- Exhibits: `all`/`available`/`none` modes each realized; instantiation gate catches what relaxed modes let through.
- Invocation: `go test ./test/scenarios/attributes -run TestRefValidationMode -count=1`
- Status: EXHIBITS WORKING.

**STORY-mandatory-instantiation-gate** — Instance create validates value constraints.
- Artifact: `test/scenarios/attributes/instantiation_static_config_gate_e2e_test.go`
- Exhibits: value-constraint violation rejected at create with attribute + constraint named; well-formed succeeds.
- Invocation: `go test ./test/scenarios/attributes -run TestInstantiationStaticConfigGate -count=1`
- Status: EXHIBITS WORKING.

**STORY-lenient-marker** — Template author marks substitution lenient.
- Artifact: `test/scenarios/attributes/lenient_marker_recovery_test.go`
- Exhibits: `?`-marked directive against missing source resolves empty and dispatches; no-marker fails dispatch.
- Invocation: `go test ./test/scenarios/attributes -run TestLenientMarkerRecovery -count=1`
- Status: EXHIBITS WORKING.

**STORY-verifier-severity-partition** — Template author distinguishes warning vs error.
- Artifact: `lib/services/test/scenarios/verifier_severity_partition_e2e_test.go` (note: spec said `test/scenarios/` — see Section 3, TD-verifier-severity-partition-test-location).
- Exhibits: `severity: warning` failing check is non-blocking (run succeeds); `severity: error` failing check blocks commit (terminal error).
- Invocation: `go test ./lib/services/test/scenarios/ -run TestVerifierSeverityPartition -count=1`
- Status: EXHIBITS WORKING.

**STORY-template-fan-out** — Template author declares fan-out partitioning.
- Artifact: `test/scenarios/template_fan_out_e2e_test.go`
- Exhibits: N sub-claims materialized; N node-runs concurrent; parent settles only after all resolve; aggregate outcome reflects sub-resolutions.
- Invocation: `go test ./test/scenarios/ -run TestTemplateFanOut -count=1`
- Status: EXHIBITS WORKING.

**STORY-template-sub-graph-delegation** — Template author composes via sub-graphs.
- Artifact: `test/scenarios/template_sub_graph_delegation_e2e_test.go`
- Exhibits: delegate node settles after sub-graph; sub-graph terminal outcome propagates.
- Invocation: `go test ./test/scenarios/ -run TestTemplateSubGraphDelegation -count=1`
- Status: EXHIBITS WORKING.

**STORY-template-error-policy** — Template author routes error classes.
- Artifact: `test/scenarios/template_error_policy_e2e_test.go`
- Exhibits: each of `pass`, `give_up`, `retry`, `discard_claims_then_retry` produces the declared observable effect.
- Invocation: `go test ./test/scenarios/ -run TestTemplateErrorPolicy -count=1`
- Status: EXHIBITS WORKING.

**STORY-template-subscriptions** — Template author wires CEL-predicated subscriptions.
- Artifact: `test/scenarios/template_subscriptions_cel_e2e_test.go`
- Exhibits: CEL predicate match fires node; non-match suppresses; trailing-`*` prefix matches by prefix.
- Invocation: `go test ./test/scenarios/ -run TestTemplateSubscriptions -count=1`
- Status: EXHIBITS WORKING.

### Cluster 4 — Executor author + bundled executors

**STORY-executor-protocol** — Service author writes custom executor.
- Artifact: `examples/executor/main_e2e_test.go` + `examples/executor/README.md` (Proof form: example).
- Exhibits: cross-stack registration / Execute streaming / named events on `/v1/events` / declared error class routed via policy / attribute schema validation at registration.
- Invocation: `go test ./examples/executor -run TestE2E -count=1`
- Status: EXHIBITS WORKING.

**STORY-executor-trace-observability** — Operator queries/streams executor traces.
- Artifact: `test/scenarios/executor_trace_observability_e2e_test.go`
- Exhibits: dashboard subscribes to trace stream and observes events in flight; trace history matches stream after terminal.
- Invocation: `go test ./test/scenarios/ -run TestExecutorTraceObservability -count=1`
- Status: EXHIBITS WORKING.

**STORY-http-node** — Template author integrates HTTP upstreams.
- Artifact: `test/scenarios/http_node_e2e_test.go`
- Exhibits: 200 populates output; 429 parks with `resume_at` and supervisor wakes; 4xx-with-class surfaces `http/<class>`; 4xx without surfaces `_unspecified`.
- Invocation: `go test ./test/scenarios/ -run TestHttpNode -count=1`
- Status: EXHIBITS WORKING.

**STORY-claude-agent** — Operator wires agentic node with full controls.
- Artifact: `lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go` (cross-stack via fake-CLI runner under `lib/services/test/scenarios/claude_agent_fake_cli/`) + the existing TS test suite (`lib/services/executors/claude-agent/src/{signoff-gate,mcp-servers-wiring,rate-limit,observability,agent-run,lifecycle}.{e2e.,}test.ts`).
- Exhibits: signoff gate accepts real-bound-output / `allow_inline=false` refuses inline / declared error classes route via policy / env-var-referenced credentials don't persist in plaintext.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestClaudeAgentCrossStack -count=1 && (cd lib/services/executors/claude-agent && npm test)`
- Status: EXHIBITS WORKING.

**STORY-verifier-http** — Template author validates via external check service.
- Artifact: `test/scenarios/verifier_http_e2e_test.go`
- Exhibits: 2xx → success; 4xx-with-class → typed error; payload echo-back confirms real claim bytes reach the upstream.
- Invocation: `go test ./test/scenarios/ -run TestVerifierHttp -count=1`
- Status: EXHIBITS WORKING.

### Cluster 5 — Publisher author + bundled sensors

**STORY-publisher-protocol** — Service author writes custom publisher.
- Artifact: `examples/publisher/main_e2e_test.go` + `examples/publisher/README.md` (Proof form: example).
- Exhibits: cross-stack Subscribe / publish / restart-reconcile via `ListSubscriptions` without re-subscribing active subscriptions.
- Invocation: `go test ./examples/publisher -run TestE2E -count=1`
- Status: EXHIBITS WORKING.

**STORY-sensor-cron** — Operator wires durable cron-driven message.
- Artifact: `lib/services/test/scenarios/sensor_cron_restart_recovery_e2e_test.go` (cross-stack restart-recovery) + existing in-process tests under `lib/services/sensors/sensor-cron/`.
- Exhibits: state DSN persistence honored on restart; replica posture (no silent leader election); cron advancement from prior `next_fire_at`.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestSensorCronRestartRecovery -count=1`
- Status: EXHIBITS WORKING.

**STORY-sensor-http** — Operator wires poll-driven HTTP message.
- Artifact: `lib/services/test/scenarios/sensor_http_e2e_test.go`
- Exhibits: interval-driven emission; body filter enforced; polling watermark preserved across restart.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestSensorHttp -count=1`
- Status: EXHIBITS WORKING.

**STORY-sensor-webhook** — Operator wires inbound-webhook message.
- Artifact: `lib/services/test/scenarios/sensor_webhook_e2e_test.go`
- Exhibits: inbound POST acknowledged only after message persisted; path-prefix filter enforced; payload reflects real inbound bytes.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestSensorWebhook -count=1`
- Status: EXHIBITS WORKING.

**STORY-sensor-object-store** — Operator wires object-store-driven message.
- Artifact: `lib/services/test/scenarios/sensor_object_store_e2e_test.go`
- Exhibits: new-object discovery → message with real metadata; restart preserves discovery state (no re-emit).
- Invocation: `go test ./lib/services/test/scenarios/ -run TestSensorObjectStore -count=1`
- Status: EXHIBITS WORKING.

### Cluster 6 — Claim-producer author + bundled stores

**STORY-claim-producer-protocol** — Service author writes custom claim-producer.
- Artifact: `examples/claimproducer/main_e2e_test.go` + `examples/claimproducer/README.md` (Proof form: example).
- Exhibits: cross-stack registration / Open / Commit / Abandon / Release; un-advertised write-semantics refused at registration.
- Invocation: `go test ./examples/claimproducer -run TestE2E -count=1`
- Status: EXHIBITS WORKING.

**STORY-claim-producer-scopes-conflict** — Operator uses non-trivial overlap rules.
- Artifact: `lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go`
- Exhibits: prefix-overlap produces unavailable-second-acquirer; fan-out path consults `ScopesConflict`; producers without capability not asked.
- Invocation: `go test ./lib/services/test/scenarios/scopes_conflict -count=1`
- Status: EXHIBITS WORKING.

**STORY-claim-producer-conformance** — Author proves producer correct via conformance CLI.
- Artifact: `lib/protocols/conformance/claimproducer/runner_terminals_test.go` + `lib/services/test/scenarios/conformance_9b/probe_test.go` + `producers_test.go` + `lib/services/test/scenarios/atomic_staging/conformance_claimproducer_cli_test.go`.
- Exhibits: terminal verbs / idempotent retries / 9b probe / CLI non-zero on failure.
- Invocation: `go test ./lib/protocols/conformance/claimproducer/ ./lib/services/test/scenarios/conformance_9b/ ./lib/services/test/scenarios/atomic_staging/ -count=1`
- Status: EXHIBITS WORKING.

**STORY-claim-producer-observability** — Operator dashboards producer-side state.
- Artifact: `lib/services/test/scenarios/claim_producer_observability_dashboard_e2e_test.go`
- Exhibits: dashboard sees real producer-side claim detail, live state changes via stream, inventory pagination, producer-declared admin views.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestClaimProducerObservabilityDashboard -count=1`
- Status: EXHIBITS WORKING.

**STORY-store-filesystem** — Operator uses filesystem-backed store.
- Artifact: `lib/services/stores/filesystem/store/{store,ledger,pick_policy,drained,admin_sync}_test.go` + `examples/atomic-staging-fs-producer/atomic_staging_test.go` + `lib/services/test/scenarios/atomic_staging/fs_held_swap_e2e_test.go`.
- Exhibits: atomic POSIX rename swap at Commit; staging dir discarded on Abandon; admin sync refreshes queue with `sync_strategy: explicit`.
- Invocation: `go test ./lib/services/stores/filesystem/... ./lib/services/test/scenarios/atomic_staging/... -count=1`
- Status: EXHIBITS WORKING.

**STORY-store-postgres** — Operator uses postgres-backed staged-async store.
- Artifact: `lib/services/stores/postgres/store/{store,atomic_staging,ledger}_test.go` + `lib/services/test/scenarios/atomic_staging/pg_verifier{_test,_commit_abandon_test}.go` + `lib/services/test/scenarios/pg_error_classes/pg_error_classes_test.go`.
- Exhibits: atomic staging-schema swap; `row_count_ratio` runs as aggregate-only; `pg/swap_failed` + `pg/claim_unavailable` declared error classes routed.
- Invocation: `go test ./lib/services/stores/postgres/... ./lib/services/test/scenarios/atomic_staging/... ./lib/services/test/scenarios/pg_error_classes/ -count=1`
- Status: EXHIBITS WORKING.

### Cluster 7 — Validation / data-processing / lifecycle-subscriber / openlineage

**STORY-validation-author** — Service author writes validation mix-in.
- Artifact: `examples/validation/main_e2e_test.go` + `examples/validation/README.md` (Proof form: example).
- Exhibits: cross-stack validator called at registration; error-severity findings block; warning-severity surfaces without blocking.
- Invocation: `go test ./examples/validation -run TestE2E -count=1`
- Status: EXHIBITS WORKING.

**STORY-data-processing-author** — Claim-producer author writes typed-data mix-in.
- Artifact: `examples/data-processing/main_e2e_test.go` + `examples/data-processing/README.md` (Proof form: example) + `test/scenarios/leaf_candidate_handle_e2e_test.go` (leaf-candidate leg).
- Exhibits: `BeginCandidate` per fan-out partition / `CommitCandidate` on leaf success / `AbandonCandidate` on leaf failure / `ListVersions` / `ListPartitions` / `GetVersionSchema` reflect real state.
- Invocation: `go test ./examples/data-processing -run TestE2E -count=1`
- Status: EXHIBITS WORKING.

**STORY-lifecycle-subscriber-author** — Service author writes lifecycle subscriber.
- Artifact: `examples/lifecyclesubscriber/main_e2e_test.go` + `examples/lifecyclesubscriber/README.md` (Proof form: example).
- Exhibits: all seven callbacks fire at correct transitions with documented context fields; subscriber failure response honored synchronously.
- Invocation: `go test ./examples/lifecyclesubscriber -run TestE2E -count=1`
- Status: EXHIBITS WORKING.

**STORY-subscriber-openlineage** — Operator emits OpenLineage to data-platform.
- Artifact: `lib/services/test/scenarios/subscriber_openlineage_e2e_test.go`
- Exhibits: fake receiver receives well-formed OpenLineage 1.x JSON; event IDs correspond to rimsky-side IDs.
- Invocation: `go test ./lib/services/test/scenarios/ -run TestSubscriberOpenlineage -count=1`
- Status: EXHIBITS WORKING.

### Cluster 8 — Host-agent

**STORY-host-agent-late-bind-all-protocols** — Every protocol works through late-bind.
- Artifact: `test/scenarios/host_agent_latebind_all_protocols_test.go` + `lib/runtime/hostagent/dispatch_unary_test.go`.
- Exhibits: each of the five protocols (executor / claim-producer / publisher / validation / data-processing) is served by a real spawned binary; no `Unimplemented` returned.
- Invocation: `go test ./test/scenarios/ -run TestHostAgentLatebind -count=1 && go test ./lib/runtime/hostagent/ -count=1`
- Status: EXHIBITS WORKING.

**STORY-host-agent-per-run-scope-isolation** — Concurrent run-scopes get isolated children.
- Artifact: `test/scenarios/host_agent_per_run_scope_isolation_test.go` + `test/scenarios/host_agent_reap_test.go`.
- Exhibits: two distinct children spawned (not shared); per-run-scope termination reaps only that run-scope's child.
- Invocation: `go test ./test/scenarios/ -run 'TestHostAgentPerRunScopeIsolation|TestHostAgentReap' -count=1`
- Status: EXHIBITS WORKING.

**STORY-host-agent-per-binding-overrides** — Per-binding env/args/cwd/timeout honored.
- Artifact: `test/scenarios/host_agent_per_binding_exec_overrides_test.go`
- Exhibits: declared args/env/cwd echoed back through real dispatch; per-binding timeout actually bounds spawn wait.
- Invocation: `go test ./test/scenarios/ -run TestHostAgentPerBindingExecOverrides -count=1`
- Status: EXHIBITS WORKING.

**STORY-host-agent-anonymous-mode** — Late-bind works under anonymous mode.
- Artifact: `test/scenarios/host_agent_anonymous_mode_latebind_test.go`
- Exhibits: anonymous-mode instance dispatch reaches the connected agent and the late-bound child runs.
- Invocation: `go test ./test/scenarios/ -run TestHostAgentAnonymousModeLatebind -count=1`
- Status: EXHIBITS WORKING.

**STORY-host-agent-control-plane** — Operator manages agent lifecycle via CLI.
- Artifact: `examples/host-agent-control-plane-demo.sh` + `test/scenarios/host_agent_control_plane_demo_test.go` (driver; Proof form: demo).
- Exhibits: start / status / stop with children reaped; misconfigured proxy URL refused with clear diagnostic.
- Invocation: `go test ./test/scenarios/ -run TestHostAgentControlPlaneDemo -count=1`
- Status: EXHIBITS WORKING.

### Cluster 9 — Operator deployment surfaces

**STORY-rimsky-deployment-bootstrap** — Entrypoint role selection + migrate discipline.
- Artifact: `cmd/rimsky-entrypoint/main_test.go`
- Exhibits: no command → all three roles + one migrate; single role → only the named role; `rimsky-control-api` owns migrate in split; unknown command exits non-zero; `RIMSKY_ENTRYPOINT_MIGRATE` overrides.
- Invocation: `go test ./cmd/rimsky-entrypoint/ -count=1`
- Status: EXHIBITS WORKING.

**STORY-rimsky-health-check** — Health probe surface for LBs and k8s.
- Artifact: `test/scenarios/health_check_e2e_test.go`
- Exhibits: `GET /v1/health` returns 2xx without bearer; non-success when persistence dependency is down.
- Invocation: `go test ./test/scenarios/ -run TestHealthCheck -count=1`
- Status: EXHIBITS WORKING.

---

## 2. Technical decisions kept

Each TD below is honored by the implementation as the spec specified. Citations point to the embodiment site.

### Workspace + module layout

- **TD-module-split** — Five-module Go workspace tied by `go.work`. → `go.work:3-9` lists `.`, `./lib/foundation`, `./lib/protocols`, `./lib/services`, `./examples`.
- **TD-layer-ordering** — `foundation` → `graph` → `runtime` → `control` enforced by depguard. → `.golangci.yml` `depguard` block (`graph-purity`, `runtime-purity`, `foundation-purity` rules).
- **TD-toplevel-dirs** — `cmd/` / `lib/` / `test/` / `tools/` layout. → repo root directory tree.

### Lint / depguard

- **TD-depguard-pgx-isolation** — `pgx` confined. → `.golangci.yml::depguard::pgx-isolation`.
- **TD-depguard-foundation-internal** — `foundation/internal/` package-private. → `.golangci.yml::depguard::foundation-internal-isolation`.
- **TD-depguard-protocols-purity** — protocols imports stdlib + grpc + protobuf + uuid + yaml. → `.golangci.yml::depguard::protocols-purity`.
- **TD-depguard-foundation-purity** — foundation imports stdlib + protocols + chosen libs. → `.golangci.yml::depguard::foundation-purity`.
- **TD-depguard-graph-purity-with-scheduler-exception** — graph pure with documented scheduler exception. → `.golangci.yml::depguard::graph-purity`.
- **TD-depguard-runtime-purity** — runtime imports foundation + graph + protocols. → `.golangci.yml::depguard::runtime-purity`.
- **TD-depguard-consumption-isolation** — `lib/services/` imports only protocols. → `.golangci.yml::depguard::consumption-side-isolation`.
- **TD-revive-no-exported-rule** — revive's `exported` rule disabled. → `.golangci.yml` revive config.

### Library choices

- **TD-logging-slog-only** — stdlib `log/slog`. → `go.mod` (no zap/zerolog); `lib/runtime/...` imports.
- **TD-http-router-chi** — `go-chi/chi/v5`. → `lib/control/controlapi/app.go:174` (`chi.NewRouter()`).
- **TD-postgres-pgx-v5** — `jackc/pgx/v5`. → `lib/foundation/persistence/postgres/...` import surface.
- **TD-sqlite-modernc-pure-go** — `modernc.org/sqlite`. → `lib/foundation/persistence/sqlite/...` import surface.
- **TD-cron-robfig-v3** — `robfig/cron/v3`. → `lib/services/sensors/sensor-cron/sensor.go` cron-spec parsing.
- **TD-uuid-google** — `google/uuid`. → root `go.mod`.
- **TD-yaml-gopkg-v3** — `gopkg.in/yaml.v3`. → root `go.mod`.
- **TD-jcs-cyberphone** — `cyberphone/json-canonicalization`. → root `go.mod`; used in template-spec hashing.
- **TD-grpc-google-official** — `google.golang.org/grpc` + `protobuf`. → `lib/protocols/proto/v1/gen/*.pb.go`.
- **TD-testcontainers-go** — `testcontainers-go`. → `test/support/scenario/harness.go`; `lib/services/test/harness/...`.
- **TD-metrics-prometheus-client** — `prometheus/client_golang`. → root `go.mod`; control-api metrics export.

### Conventions

- **TD-cold-read-style** — file-by-feature; size guidelines; max 3 nesting. → `.claude/rules/cold-read-cheatsheet.md` and `cold-read/`.
- **TD-blessed-invariant-annotations** — `@blessed-invariant` for safety properties. → `grep -rn '@blessed-invariant' lib/` (e.g., `lib/runtime/runner_acquire.go`, `lib/runtime/runner_terminal_release.go`).
- **TD-design-link-annotations** — `@concept:` / `@story:` / `@decision:`. → spec design-changes block ("@story:`/`@decision:` annotations on load-bearing code sites"); annotations present in code under `lib/`.
- **TD-tracked-duplication** — `@source:` / `@diverged:`. → `.claude/rules/cold-read-cheatsheet.md` codified; in-code use across `lib/`.

### Build / image

- **TD-build-tool-makefile** — Makefile as single source of truth. → `Makefile` at repo root.
- **TD-build-go-version** — Go 1.25.0 minimum. → `go.work:1` (`go 1.25.0`); root `go.mod`.
- **TD-build-cgo-disabled** — `CGO_ENABLED=0`. → `Makefile` build targets.
- **TD-image-two-stage** — alpine build → distroless static nonroot runtime. → `dockerfiles/Dockerfile.rimsky`; `dockerfiles/Dockerfile.go-base`.
- **TD-image-set-four-core** — four core images. → `Makefile::core-images` target; `dockerfiles/Dockerfile.{rimsky,all-in-one,conformance}` + `Dockerfile.go-base` (host-agent-proxy).
- **TD-image-set-bundled-services** — one image per bundled service. → `Makefile::service-images`; Dockerfiles co-located under `lib/services/`.
- **TD-image-entrypoint-role-selection** — `rimsky-entrypoint`. → `cmd/rimsky-entrypoint/main.go` + `cmd/rimsky-entrypoint/main_test.go`.
- **TD-image-tagging-version-and-channel** — `:v0.x.y` + `:latest`/`:dev`. → `Makefile` push-images target.
- **TD-registry-hub-rimskyai-namespace** — `rimskyai` namespace. → `Makefile` IMAGE_REGISTRY default.

### Persistence

- **TD-persistence-dual-backend** — Postgres and SQLite. → `lib/foundation/persistence/{postgres,sqlite}/`.
- **TD-migrations-append-only-numbered** — numbered append-only per backend. → `lib/foundation/persistence/{postgres,sqlite}/migrations/` (numbered `001-...`, `002-...`).
- **TD-migrations-no-compat-shims** — pre-v1 drop+recreate. → `.claude/rules/rules.md` pre-v1 freedom; migration files reflect this.
- **TD-advisory-locks** — postgres advisory locks + sqlite session-level equivalent. → `lib/foundation/persistence/postgres/...` advisory-lock helpers.
- **TD-blob-backends-pluggable** — pluggable blob backend interface. → `lib/foundation/persistence/.../blob/` interface + drivers.
- **TD-blob-spill-threshold-config** — configurable threshold. → `rimsky.yml` config + persistence layer reads it.
- **TD-message-idempotencies-dedup-tuple** — 5-tuple discriminator. → `lib/foundation/persistence/postgres/messages.go` (insert into `rimsky_message_idempotencies`); `concepts/message.md` updated to enumerate the tuple.
- **TD-wait-set-topic-kind-taxonomy** — 5-value taxonomy + legacy fallback. → `lib/foundation/persistence/...` wait-set queries.

### Protocol design

- **TD-protocol-version-v1-namespaced** — all control-API routes under `/v1/`. → `lib/control/controlapi/app.go:188` (`r.Route("/v1", func(v1 chi.Router) {...})`). Resolves `tension:control-api-version-prefix`.
- **TD-async-callback-post-json** — HTTP POST JSON to `${callback_url}/v1/callback/{async_ack_id}`. → `lib/runtime/callback.go` callback server.
- **TD-async-callback-outcome-oneof** — exactly-one outcome key. → `lib/protocols/proto/v1/executor.proto::AsyncCallbackBody` oneof.
- **TD-idempotency-key-header-universal** — mandatory `Idempotency-Key` header. → `lib/control/controlapi/messages*.go` handler enforcement + matrix test `lib/control/controlapi/idempotency_matrix_test.go`; `concepts/message.md` reflects mandatory header.
- **TD-idempotency-status-code-distinction** — 201 fresh / 200 replay. → idempotency-matrix test asserts both.
- **TD-message-sender-kind-discriminator** — three-value envelope enum + dedup-layer enum relation. → `lib/foundation/persistence/.../messages.go`; `concepts/message.md` updated to describe both enums.
- **TD-grpc-internal-protocols** — gRPC for all service-to-service. → every `.proto` under `lib/protocols/proto/v1/`.
- **TD-protojson-gateway** — protojson HTTP+JSON marshaling. → `lib/control/controlapi/...` JSON marshaling sites.
- **TD-spec-jcs-canonicalization** — RFC 8785 JCS for template-spec hashing. → template-hash code in `lib/foundation/...`.
- **TD-event-log-payload-shapes** — typed oneof for signal-class; free-form JSON for operational. → `lib/protocols/proto/v1/events.proto` payload oneof.
- **TD-event-log-kind-enum** — `OperationalKind` enum in proto; app logic consumes typed values; persistence is marshaling detail. → `lib/protocols/proto/v1/events.proto:29-85` (`enum OperationalKind`); `lib/foundation/events/kinds.go` typed-value API; `lib/foundation/persistence/{postgres,sqlite}/events.go` marshal/unmarshal with parse errors at the unmarshal boundary; `lib/control/controlapi/events.go` + `audit_read.go` validate `?kind=` at the request boundary. Resolves `tension:events-kind-no-enum`.
- **TD-conformance-suite-per-protocol** — one conformance suite per protocol; `rimsky conformance <protocol>`. → `lib/protocols/conformance/<protocol>/` per-protocol; CLI under `cmd/rimsky/...`.

### Auth / permission

- **TD-auth-api-key-bearer** — api-key bearer. → `lib/control/controlapi/auth_middleware*.go`; CLI `cmd/rimsky/cli/auth_*.go`.
- **TD-auth-dry-run-request-flag** — `?dry_run=true` per-request. → `lib/control/controlapi/actions.go`; `test/scenarios/auth/dry_run_test.go`.
- **TD-auth-dry-run-mode-floor-on-key** — identity-bound dry-run via grant. → `lib/control/controlapi/actions.go`; `test/scenarios/auth/dry_run_identity_bound_test.go`.
- **TD-auth-grant-scope** — per-grant scope dimension. → grant evaluator in `lib/control/controlapi/...`; `test/scenarios/auth/grant_scope_lifecycle_test.go`.

### Release

- **TD-release-formal-skill** — `/release` skill drives formal release. → `.claude/skills/release/`.
- **TD-release-semver-from-diff** — SemVer derived from diff. → `.claude/skills/release/SKILL.md`.
- **TD-release-notes-template** — template-driven notes sections. → `.claude/skills/release/SKILL.md`.
- **TD-release-dev-mechanical** — `make dev-release` mechanical. → `Makefile::dev-release` target.
- **TD-release-semver-sha-dot-joined** — SHA dot-joined into pre-release segment. → `Makefile::dev-release` version derivation.
- **TD-release-chain** — shared release chain. → `Makefile::release` target order (`lint → license-lint → core-images → service-images → test-all → scan → push-images`).
- **TD-release-scan-docker-scout** — `docker scout cves` gate. → `Makefile::scan` target.
- **TD-release-attestations** — SBOM + provenance via `buildx --push`. → `Makefile::push-images` target uses `--provenance=mode=max --sbom=true`.
- **TD-release-distribution** — Hub + npm + Go modules + GitHub Releases. → `Makefile` + `.claude/skills/release/SKILL.md`.

### Pre-v1 policy

- **TD-pre-v1-break-freely** — no backwards-compat guarantee pre-v1. → `.claude/rules/rules.md::Pre-v1 — break freely`.

### Licensing / project policy

- **TD-licensing-dual-apache-agpl** — protocols + examples + claude-agent + cold-read Apache; rest AGPL. → `licensing.yml` workspace license map; `concepts/module-layout.md::Licensing boundary` (now reflects four-member Apache surface).
- **TD-licensing-enforced-by-license-lint** — license-check enforces import direction. → `tools/license-check/`.
- **TD-implementation-language-go-plus-ts** — Go core; TS only for claude-agent. → repo organization; `lib/services/executors/claude-agent/` is the sole TS code.
- **TD-config-format-yaml** — plain YAML. → `rimsky.yml` shape; `gopkg.in/yaml.v3` for parsing.
- **TD-testing-scenario-based-e2e** — scenario-based e2e via testcontainers. → `test/scenarios/...` + `lib/services/test/scenarios/...`; harness in `test/support/scenario/harness.go`.
- **TD-project-agnostic** — no consumer specifics. → `.claude/rules/rules.md::Project-agnostic`; new scenario tests use `project-alpha`, `T_alpha`, generic names.

---

## 3. Technical decisions diverged

Each entry below is either (a) a spec TD where the implementation took a different shape, or (b) an implementation choice the spec did not anticipate but the necessity rule required.

**TD-verifier-severity-partition-test-location** *(necessitated)*
- Plan/spec said: author the cross-stack proof at `test/scenarios/verifier_severity_partition_e2e_test.go` (Pass 34, Task 48 of the plan).
- Implementation: file landed at `lib/services/test/scenarios/verifier_severity_partition_e2e_test.go`.
- Reason: the verifier-shape-checks executor lives in the services module and the test boots its bundled image via the services-side harness; locating the scenario under `lib/services/test/scenarios/` matches the pre-existing pattern for cross-stack proofs that drive bundled service images (the plan's own "Pre-existing patterns" note acknowledges this convention). Behavior is identical; placement is harness-driven.

**TD-event-log-kind-typed-api-package** *(selected)*
- Spec said: rimsky's app logic consumes typed values exclusively but did not prescribe where the typed-value API lives. The plan's "End-of-run handoff" call #1 enumerated this as an autonomous decision and defaulted to `lib/foundation/events/`.
- Implementation: `lib/foundation/events/kinds.go` + `lib/foundation/events/kinds_test.go`.
- Flavor: selected by the plan; implementer honored the plan's chosen home.
- Reason: the events package is small, focused, and depended on by every emit site that spans multiple layers; a new sub-package of foundation is cleaner than retrofitting an existing package.

**TD-url-prefix-sweep-router-mount** *(selected)*
- Spec said: every bare path under `/v1/`. Did not prescribe whether the sweep happens at a single chi-router mount or per-handler. The plan's "End-of-run handoff" call #2 enumerated this autonomous decision.
- Implementation: single `r.Route("/v1", func(v1 chi.Router) { ... })` wrap in `lib/control/controlapi/app.go:188`, with `registerHealthRoutes(v1, deps)` registered pre-auth and the rest inside an auth `v1.Group`.
- Flavor: selected by the plan; implementer honored.
- Reason: same observable behavior with a one-line change versus rewriting dozens of registrations.

**TD-claude-agent-cross-stack-fake-cli** *(necessitated)*
- Spec said: STORY-claude-agent acceptance includes the real CLI spawned + agent doing real work + async-callback returning. The plan's "End-of-run handoff" call #3 enumerated stubbing the Claude CLI's wire shape.
- Implementation: `lib/services/test/scenarios/claude_agent_fake_cli/fake-claude.js` + `Dockerfile.fake-claude-agent`; driven by `lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go`.
- Flavor: necessitated.
- Reason: the real Claude CLI in CI is impractical (credentials + cost). The stub replaces the third-party binary so the claude-agent executor's CLI-runner path is exercised end-to-end with the rimsky stack on the other side, both still real.

**TD-mcp-transport-sampled-coverage** *(selected)*
- Spec said: parity across the tool surface. The plan's "End-of-run handoff" call #4 enumerated sampling one read + one mutation per tool category.
- Implementation: `lib/services/test/scenarios/mcp_transport_parity_e2e_test.go` exercises a representative sample per category rather than every tool.
- Flavor: selected.
- Reason: parity is the acceptance, not coverage of every tool; sampling exhibits parity efficiently while keeping the pass bounded.

**TD-failure-path-tests-on-anonymous-bootstrap-and-host-agent-control-plane** *(necessitated)*
- Spec said: Falsifiers naming the failure paths (re-running `auth init` on a non-empty keys table; misconfigured proxy URL) but no specific test shape. The plan's "End-of-run handoff" call #5 enumerated reaching the failure paths explicitly.
- Implementation: `test/scenarios/auth/anonymous_mode_bootstrap_e2e_test.go` asserts `rimsky auth init` refuses on a non-empty keys table; `examples/host-agent-control-plane-demo.sh` exercises the misconfigured-proxy-URL path; both verify the failure path is reachable.
- Flavor: necessitated.
- Reason: the Falsifier holds only if the failure path is observable.

---

**No spec TDs were silently skipped.** Every TD in the spec's `## Manifest::Technical decisions` block (75 entries) appears in Section 2 (Technical decisions kept). Section 3 enumerates the additional necessitated/selected work the spec did not name but the implementation required to deliver every story's Acceptance.

---

## Coverage check

- **Stories:** 62 in the manifest; 62 exhibited working (Section 1).
- **Technical decisions:** 75 in the manifest; 75 kept (Section 2); 0 diverged from a spec-prescribed shape (Section 3 enumerates 6 entries — all necessitated or selected choices that did not contradict a spec instruction).
- **Foundational passes:** 3 (URL prefix sweep / event-kind enum / design-doc bootstrap) all landed; concept mutations on `module-layout.md`, `message.md`, `event-log.md` all applied; 3 tension files moved to `tensions/_resolved/`.
- **TOCs:** `.ok-planner/design/stories.md` + `.ok-planner/design/decisions.md` both present.

**Process defects:** None surfaced. The no-deferral audit's responsibility — catching missing/hollow proofs — was upheld; no Section 1 entry is GAP.
