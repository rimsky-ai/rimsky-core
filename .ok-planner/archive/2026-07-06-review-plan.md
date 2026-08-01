# Rimsky Full-Review — Salvage & Continuation Plan

Date opened: 2026-07-06. **Working document** for finishing the full fan-out review. The first attempt (workflow `wf_a0a27351-1bf`) tripped the session usage limit partway and lost most of its fleet; an orchestration bug also left the code-review, decision, and story tiers unscheduled. Salvaged output — **436 unverified concept-currency findings** — is in `review-findings-2026-07-06.csv` (37 of 73 concept docs covered). This plan drains the remainder in small resumable **batches**.

## Files

- `review-findings-2026-07-06.csv` — findings ledger. Columns: `id,severity,category,file,line,summary,evidence,verdict,verified_severity,verify_note`. New findings **append** here.
- `review-plan-2026-07-06.md` — this document. The **Backlog** table is the source of truth for what remains and what is done.

## Status legend

`TODO` not started · `WIP` an agent is on it · `DONE` findings appended & row marked · `SKIP` intentionally not run (reason in Notes).

## Unit of execution: the BATCH

A **batch** = 4 waves ≈ 20 finder agents — sized to stay well under the ~37-agent / ~5M-token cliff that killed the first run. Run **one batch per sitting**; each ends on a clean wave boundary, so a session reset costs nothing. Say "run Batch N" to go.

### How a batch runs (protocol)

1. Take the lowest-numbered batch with `TODO` rows. Dispatch its waves in order — one finder agent per row, ~5 per wave, parallel within the wave.
2. Finders return structured findings only (file, line, category, severity, summary, evidence). They do **not** edit any shared file.
3. After each wave returns, the orchestrator appends findings to the CSV with `verdict=unverified` and flips those rows `TODO`→`DONE` (or `SKIP` + reason).
4. At the batch boundary, report counts and stop. Next batch is a fresh turn.

### Finder scope contract (paste into each agent)

> Repo `/Users/patrick/Documents/projects/research/rimsky/rimsky-core` (rimsky, Go). Design corpus `.ok-planner/design/` is the durable model — read it freely. Ruling: only a message runs a frame; every message gets a new frame; a node's attributes at frame start are the schema defaults; message payloads are the ONLY cross-frame carrier; the frame row is cascade-immutable (reaper stamps `ended_at` once, scheduler heartbeats `last_progress_at`). Report **every** issue — no cap, no ranking cutoff — but don't pad; return nothing for a clean unit. For each drift finding name **which side is stale** (code or doc) and why. Never read `lib/protocols/proto/v1/gen`, `vendor`, `.git`, `bin`, `tmp`. Severity: critical=data loss/corruption/crash/security/broken load-bearing invariant; major=wrong behavior, real bug, a doc claim that steers a reader wrong, or a missing test for a load-bearing invariant; minor=quality/clarity/gaps; nit=cosmetic.

## Batch roadmap

| Batch | Waves | Chunks | Phase | Covers | Status |
|---|---|---|---|---|---|
| **1** | 1–4 | 20 | Phase 1 · corpus | 13×concept-currency, 7×global-sweep | DONE (94 found, 93 confirmed) |
| **2** | 5–8 | 20 | Phase 1 · corpus | 20×concept-currency | DONE (65 found, 60 confirmed) |
| **3** | 9–12 | 20 | Phase 1 · corpus / Phase 2 · code | 17×code-review, 3×concept-currency | DONE (140 found, 132 confirmed) |
| **4** | 13–16 | 20 | Phase 2 · code | 20×code-review | DONE (453 found; majors verifying) |
| **5** | 17–20 | 20 | Phase 2 · code | 20×code-review | DONE (302 found; 75/81 crit-major confirmed) |
| **6** | 21–24 | 20 | Phase 2 · code | 20×code-review | DONE (346 found; 104/112 crit-major confirmed) |
| **7** | 25–28 | 20 | Phase 2 · code | 20×code-review | TODO |
| **8** | 29–32 | 20 | Phase 2 · code / Phase 3 · docs | 13×code-review, 7×decision-doc | TODO |
| **9** | 33–36 | 20 | Phase 3 · docs | 20×decision-doc | TODO |
| **10** | 37–40 | 19 | Phase 3 · docs | 1×decision-doc, 15×story-doc, 3×tension-validity | TODO |

**Inline verification (Batches 1–10):** each finder batch is now followed by a verification pass over its own findings before we move on (default-refute verifiers, ~10 findings per agent, writing `verdict`/`verified_severity`/`verify_note` into the CSV). Batches 1 and 2 are done and verified (153/159 confirmed).

### Verification pass — salvaged findings (V1–V4)

The 433 findings salvaged from the crashed first run (CSV `source=concept-run-1`, ids 4–436) never got inline verification. They are verified as their own batched pass, one V-batch per sitting (~11 default-refute verifiers each), independent of the finder batches — interleave them however is convenient. Expect some `DUPLICATE` verdicts against Batch 1/2 (both passes covered the concept catalog).

| V-Batch | CSV id range | Findings | Verifiers | Status |
|---|---|---|---|---|
| **V1** | 4–112 | 109 | ~11 | DONE |
| **V2** | 113–221 | 109 | ~11 | DONE |
| **V3** | 222–330 | 109 | ~11 | DONE |
| **V4** | 331–436 | 106 | ~11 | DONE |

## Backlog

Total remaining chunks: **199** across **10** batches (Phase 4 verification additional).

| Batch | Wave | Chunk | Phase/Lens | Scope | Status | Notes |
|---|---|---|---|---|---|---|
| 1 | 2 | `K-node-subscription` | concept-currency | .ok-planner/design/concepts/node-subscription.md | DONE |  |
| 1 | 2 | `K-node` | concept-currency | .ok-planner/design/concepts/node.md | DONE |  |
| 1 | 2 | `K-message` | concept-currency | .ok-planner/design/concepts/message.md | DONE |  |
| 1 | 3 | `K-message-sender-node` | concept-currency | .ok-planner/design/concepts/message-sender-node.md | DONE |  |
| 1 | 3 | `K-named-lock` | concept-currency | .ok-planner/design/concepts/named-lock.md | DONE |  |
| 1 | 3 | `K-lineage` | concept-currency | .ok-planner/design/concepts/lineage.md | DONE |  |
| 1 | 3 | `K-message-schema` | concept-currency | .ok-planner/design/concepts/message-schema.md | DONE |  |
| 1 | 3 | `K-observability` | concept-currency | .ok-planner/design/concepts/observability.md | DONE |  |
| 1 | 4 | `K-orphan-reaper` | concept-currency | .ok-planner/design/concepts/orphan-reaper.md | DONE |  |
| 1 | 4 | `K-instance` | concept-currency | .ok-planner/design/concepts/instance.md | DONE |  |
| 1 | 4 | `K-parked-state` | concept-currency | .ok-planner/design/concepts/parked-state.md | DONE |  |
| 1 | 4 | `K-lineage-record` | concept-currency | .ok-planner/design/concepts/lineage-record.md | DONE |  |
| 1 | 4 | `K-module-layout` | concept-currency | .ok-planner/design/concepts/module-layout.md | DONE |  |
| 1 | 1 | `W-indexes` | global-sweep | indexes | DONE |  |
| 1 | 1 | `W-retired-vocab` | global-sweep | retired-vocab | DONE |  |
| 1 | 1 | `W-claude-md` | global-sweep | claude-md | DONE |  |
| 1 | 1 | `W-module-boundaries` | global-sweep | module-boundaries | DONE |  |
| 1 | 1 | `W-uniformity` | global-sweep | uniformity | DONE |  |
| 1 | 2 | `W-citations` | global-sweep | citations | DONE |  |
| 1 | 2 | `W-schema-vs-docs` | global-sweep | schema-vs-docs | DONE |  |
| 2 | 5 | `K-permission` | concept-currency | .ok-planner/design/concepts/permission.md | DONE |  |
| 2 | 5 | `K-persistence-database` | concept-currency | .ok-planner/design/concepts/persistence-database.md | DONE |  |
| 2 | 5 | `K-publisher-subscription` | concept-currency | .ok-planner/design/concepts/publisher-subscription.md | DONE |  |
| 2 | 5 | `K-node-run` | concept-currency | .ok-planner/design/concepts/node-run.md | DONE |  |
| 2 | 5 | `K-publisher` | concept-currency | .ok-planner/design/concepts/publisher.md | DONE |  |
| 2 | 6 | `K-replica` | concept-currency | .ok-planner/design/concepts/replica.md | DONE |  |
| 2 | 6 | `K-rimsky-yml` | concept-currency | .ok-planner/design/concepts/rimsky-yml.md | DONE |  |
| 2 | 6 | `K-rimsky` | concept-currency | .ok-planner/design/concepts/rimsky.md | DONE |  |
| 2 | 6 | `K-role-template` | concept-currency | .ok-planner/design/concepts/role-template.md | DONE |  |
| 2 | 6 | `K-run-scope` | concept-currency | .ok-planner/design/concepts/run-scope.md | DONE |  |
| 2 | 7 | `K-sensor` | concept-currency | .ok-planner/design/concepts/sensor.md | DONE |  |
| 2 | 7 | `K-service` | concept-currency | .ok-planner/design/concepts/service.md | DONE |  |
| 2 | 7 | `K-signal` | concept-currency | .ok-planner/design/concepts/signal.md | DONE |  |
| 2 | 7 | `K-sub-graph` | concept-currency | .ok-planner/design/concepts/sub-graph.md | DONE |  |
| 2 | 7 | `K-supervisor` | concept-currency | .ok-planner/design/concepts/supervisor.md | DONE |  |
| 2 | 8 | `K-tag` | concept-currency | .ok-planner/design/concepts/tag.md | DONE |  |
| 2 | 8 | `K-template` | concept-currency | .ok-planner/design/concepts/template.md | DONE |  |
| 2 | 8 | `K-terminal-resolution` | concept-currency | .ok-planner/design/concepts/terminal-resolution.md | DONE |  |
| 2 | 8 | `K-terminal-tag` | concept-currency | .ok-planner/design/concepts/terminal-tag.md | DONE |  |
| 2 | 8 | `K-validation` | concept-currency | .ok-planner/design/concepts/validation.md | DONE |  |
| 3 | 9 | `C000` | code-review (both lenses) | cmd/internal/bundledwire, cmd/rimsky, cmd/rimsky-control-api, cmd/rimsky-entrypoint | DONE |  |
| 3 | 9 | `C001` | code-review (both lenses) | cmd/rimsky-host-agent | DONE |  |
| 3 | 10 | `C002` | code-review (both lenses) | cmd/rimsky-host-agent-proxy | DONE |  |
| 3 | 10 | `C003` | code-review (both lenses) | cmd/rimsky-migrate, cmd/rimsky-scheduler, cmd/rimsky-supervisor | DONE |  |
| 3 | 10 | `C004` | code-review (both lenses) | cmd/rimsky/cli/{admin.go,admin_test.go,agent.go,agent_process_unix.go,agent_process_win… | DONE |  |
| 3 | 10 | `C005` | code-review (both lenses) | cmd/rimsky/cli/{client.go,client_errors.go,client_test.go,config.go,config_test.go,cont… | DONE |  |
| 3 | 10 | `C006` | code-review (both lenses) | cmd/rimsky/cli/{flags_internal_test.go,health.go,health_test.go,instances.go,instances_… | DONE |  |
| 3 | 11 | `C007` | code-review (both lenses) | cmd/rimsky/cli/{tags_test.go,templates.go,templates_source_file_test.go,templates_test.… | DONE |  |
| 3 | 11 | `C008` | code-review (both lenses) | cmd/rimsky/cli/compose/{apply.go,apply_test.go,artifact.go,artifact_swap_darwin.go,arti… | DONE |  |
| 3 | 11 | `C009` | code-review (both lenses) | cmd/rimsky/cli/compose/{manifest_test.go,plan.go,plan_test.go,progress.go,progress_test… | DONE |  |
| 3 | 11 | `C010` | code-review (both lenses) | cmd/rimsky/cli/compose/{run_test.go,shutdown.go,shutdown_test.go,state.go,state_test.go… | DONE |  |
| 3 | 11 | `C011` | code-review (both lenses) | cmd/rimsky/cli/compose/testdata/stub-executor, cmd/rimsky/cli/internal/clitest, cmd/rim… | DONE |  |
| 3 | 12 | `C012` | code-review (both lenses) | examples/atomic-staging-fs-producer/sweep, examples/claimproducer, examples/data-proces… | DONE |  |
| 3 | 12 | `C013` | code-review (both lenses) | examples/lifecyclesubscriber, examples/publisher, examples/validation | DONE |  |
| 3 | 12 | `C014` | code-review (both lenses) | lib/control/config | DONE |  |
| 3 | 12 | `C015` | code-review (both lenses) | lib/control/controlapi/{actions.go,actions_test.go,admin_diagnostics.go,admin_diagnosti… | DONE |  |
| 3 | 12 | `C016` | code-review (both lenses) | lib/control/controlapi/{app_test.go,app_util.go,assets.go,assets_test.go,attribute_over… | DONE |  |
| 3 | 9 | `K-wait-set` | concept-currency | .ok-planner/design/concepts/wait-set.md | DONE |  |
| 3 | 9 | `K-transition-reason` | concept-currency | .ok-planner/design/concepts/transition-reason.md | DONE |  |
| 3 | 9 | `K-write-semantics` | concept-currency | .ok-planner/design/concepts/write-semantics.md | DONE |  |
| 4 | 13 | `C017` | code-review (both lenses) | lib/control/controlapi/{audit_read.go,audit_read_test.go,auth.go,auth_banner.go,auth_ha… | DONE |  |
| 4 | 13 | `C018` | code-review (both lenses) | lib/control/controlapi/{debug_override.go,debug_override_test.go,dryrun.go,events.go,ev… | DONE |  |
| 4 | 13 | `C019` | code-review (both lenses) | lib/control/controlapi/{instance_terminator_test.go,instances.go,instances_static_confi… | DONE |  |
| 4 | 13 | `C020` | code-review (both lenses) | lib/control/controlapi/{lineage.go,lineage_test.go,mcp_resources.go,mcp_resources_test.… | DONE |  |
| 4 | 13 | `C021` | code-review (both lenses) | lib/control/controlapi/{messages_test.go,nodes.go,nodes_tag_filter_test.go,nodes_test.g… | DONE |  |
| 4 | 14 | `C022` | code-review (both lenses) | lib/control/controlapi/{templates_test.go,validation_pipeline_test.go} | DONE |  |
| 4 | 14 | `C023` | code-review (both lenses) | lib/control/controlapi/mcp | DONE |  |
| 4 | 14 | `C024` | code-review (both lenses) | lib/control/launch | DONE |  |
| 4 | 14 | `C025` | code-review (both lenses) | lib/control/observability | DONE |  |
| 4 | 14 | `C026` | code-review (both lenses) | lib/foundation/auth, lib/foundation/cascade, lib/foundation/events, lib/foundation/inte… | DONE |  |
| 4 | 15 | `C027` | code-review (both lenses) | lib/foundation/locks, lib/foundation/locks/storetest, lib/foundation/matcher | DONE |  |
| 4 | 15 | `C028` | code-review (both lenses) | lib/foundation/persistence | DONE |  |
| 4 | 15 | `C029` | code-review (both lenses) | lib/foundation/persistence/conformance/{acquisition.go,api_keys.go,auto_terminal.go,cas… | DONE |  |
| 4 | 15 | `C030` | code-review (both lenses) | lib/foundation/persistence/conformance/{conformance_test.go,coordinator.go,dispatch.go,… | DONE |  |
| 4 | 15 | `C031` | code-review (both lenses) | lib/foundation/persistence/conformance/{node_attributes_merge_delta.go,node_attributes_… | DONE |  |
| 4 | 16 | `C032` | code-review (both lenses) | lib/foundation/persistence/conformance/{run_state_writes_isolated_by_scope.go,select_ca… | DONE |  |
| 4 | 16 | `C033` | code-review (both lenses) | lib/foundation/persistence/postgres/{advisory_locker.go,api_keys.go,backend.go,blob_lar… | DONE |  |
| 4 | 16 | `C034` | code-review (both lenses) | lib/foundation/persistence/postgres/{claim_handles.go,claim_holders.go,database.go,even… | DONE |  |
| 4 | 16 | `C035` | code-review (both lenses) | lib/foundation/persistence/postgres/{lineage.go,message_idempotencies.go,messages.go,me… | DONE |  |
| 4 | 16 | `C036` | code-review (both lenses) | lib/foundation/persistence/postgres/{publisher_subscriptions.go,queue.go,queue_park.go,… | DONE |  |
| 5 | 17 | `C037` | code-review (both lenses) | lib/foundation/persistence/postgres/{wait_set.go,wait_set_topic_kind_test.go} | DONE |  |
| 5 | 17 | `C038` | code-review (both lenses) | lib/foundation/persistence/postgres/migrations | DONE |  |
| 5 | 17 | `C039` | code-review (both lenses) | lib/foundation/persistence/sqlite/{advisory_flock_unix.go,advisory_flock_windows.go,adv… | DONE |  |
| 5 | 17 | `C040` | code-review (both lenses) | lib/foundation/persistence/sqlite/{claim_handles.go,claim_holders.go,database.go,databa… | DONE |  |
| 5 | 17 | `C041` | code-review (both lenses) | lib/foundation/persistence/sqlite/{frames.go,frames_parked_hold_test.go,frames_retentio… | DONE |  |
| 5 | 18 | `C042` | code-review (both lenses) | lib/foundation/persistence/sqlite/{messages_test.go,migrate.go,migrate_test.go,node_att… | DONE |  |
| 5 | 18 | `C043` | code-review (both lenses) | lib/foundation/persistence/sqlite/{observability_test.go,publisher_subscriptions.go,que… | DONE |  |
| 5 | 18 | `C044` | code-review (both lenses) | lib/foundation/persistence/sqlite/{supervisors.go,template_tags.go,templates.go,testacc… | DONE |  |
| 5 | 18 | `C045` | code-review (both lenses) | lib/foundation/persistence/sqlite/migrations, lib/foundation/pgpool, lib/foundation/sha… | DONE |  |
| 5 | 18 | `C046` | code-review (both lenses) | lib/foundation/spec, lib/graph/attribute | DONE |  |
| 5 | 19 | `C047` | code-review (both lenses) | lib/graph/frame | DONE |  |
| 5 | 19 | `C048` | code-review (both lenses) | lib/graph/node/{backoff.go,backoff_test.go,extract_payload_tag_literals_test.go,hard_de… | DONE |  |
| 5 | 19 | `C049` | code-review (both lenses) | lib/graph/node/{template_validator.go} | DONE |  |
| 5 | 19 | `C050` | code-review (both lenses) | lib/graph/node/{template_validator_graphs.go,template_validator_graphs_test.go,template… | DONE |  |
| 5 | 19 | `C051` | code-review (both lenses) | lib/graph/node/{template_validator_test.go} | DONE |  |
| 5 | 20 | `C052` | code-review (both lenses) | lib/graph/scheduler, lib/graph/scratch, lib/graph/shared, lib/graph/template/canonical,… | DONE |re-run after rate-limit kill; confirmed pure-cascade duplicate-enqueue critical|
| 5 | 20 | `C053` | code-review (both lenses) | lib/protocols/conformance/blobbackend, lib/protocols/conformance/claimproducer, lib/pro… | DONE |  |
| 5 | 20 | `C054` | code-review (both lenses) | lib/protocols/conformance/executor, lib/protocols/conformance/executor/scenarios, lib/p… | DONE |  |
| 5 | 20 | `C055` | code-review (both lenses) | lib/protocols/conformance/validation, lib/protocols/lifecycle, lib/protocols/publisherk… | DONE |  |
| 5 | 20 | `C056` | code-review (both lenses) | lib/runtime/{abandon_claim.go,abandon_claim_test.go,attribute_cascade.go,attribute_over… | DONE |  |
| 6 | 21 | `C057` | code-review (both lenses) | lib/runtime/{auto_terminal_test.go,breakpoint_eval.go,breakpoint_eval_test.go,breakpoin… | DONE |  |
| 6 | 21 | `C058` | code-review (both lenses) | lib/runtime/{breakpoint_resume_test.go,breakpoint_snapshot.go,callback.go,callback_adve… | DONE |  |
| 6 | 21 | `C059` | code-review (both lenses) | lib/runtime/{child_execution.go,claim_scope_conflict_committed_durable_test.go,conducto… | DONE |  |
| 6 | 21 | `C060` | code-review (both lenses) | lib/runtime/{keepalive_test.go,lifecycle_fanout.go,lineage_writer.go,lineage_writer_tes… | DONE |  |
| 6 | 21 | `C061` | code-review (both lenses) | lib/runtime/{publishers.go,pure_cascade_settle.go,retention_sweeps.go,retention_sweeps_… | DONE |  |
| 6 | 22 | `C062` | code-review (both lenses) | lib/runtime/{runner_acquire_claims.go,runner_acquire_helpers.go,runner_acquire_helpers_… | DONE |  |
| 6 | 22 | `C063` | code-review (both lenses) | lib/runtime/{runner_dispatch.go,runner_dispatch_test.go,runner_error_policy.go,runner_e… | DONE |  |
| 6 | 22 | `C064` | code-review (both lenses) | lib/runtime/{runner_lifecycle.go,runner_locks.go,runner_send_message.go,runner_send_mes… | DONE |  |
| 6 | 22 | `C065` | code-review (both lenses) | lib/runtime/{runner_terminal.go,runner_terminal_handlers.go,runner_terminal_park.go,run… | DONE |  |
| 6 | 22 | `C066` | code-review (both lenses) | lib/runtime/{subgraph_caller_lineage_test.go,subgraph_dispatch.go,subgraph_dispatch_tes… | DONE |  |
| 6 | 23 | `C067` | code-review (both lenses) | lib/runtime/{terminal_decision_cancel.go,terminal_decision_forensics.go,terminal_decisi… | DONE |  |
| 6 | 23 | `C068` | code-review (both lenses) | lib/runtime/claimproducer, lib/runtime/clientiface, lib/runtime/executor, lib/runtime/e… | DONE |  |
| 6 | 23 | `C069` | code-review (both lenses) | lib/runtime/executor/builtin/loop_counter, lib/runtime/executor/builtin/send_message | DONE |  |
| 6 | 23 | `C070` | code-review (both lenses) | lib/runtime/hostagent, lib/runtime/hostagent/testdata/stub-no-bind, lib/runtime/hostage… | DONE |  |
| 6 | 23 | `C071` | code-review (both lenses) | lib/runtime/hostagent/testdata/stubchild, lib/runtime/peer, lib/services/bundled, lib/s… | DONE |  |
| 6 | 24 | `C072` | code-review (both lenses) | lib/services/claim_producers/filesystem/server | DONE |  |
| 6 | 24 | `C073` | code-review (both lenses) | lib/services/claim_producers/filesystem/store | DONE |  |
| 6 | 24 | `C074` | code-review (both lenses) | lib/services/claim_producers/postgres/cmd, lib/services/claim_producers/postgres/lifecy… | DONE |  |
| 6 | 24 | `C075` | code-review (both lenses) | lib/services/claim_producers/postgres/store, lib/services/claim_producers/shared/listarray | DONE |  |
| 6 | 24 | `C076` | code-review (both lenses) | lib/services/claim_producers/shared/sql-checks | DONE |  |
| 7 | 25 | `C077` | code-review (both lenses) | lib/services/executors/claude-agent/{agentrun.go,agentrun_test.go,cliconfigerror.go,cli… | DONE |  |
| 7 | 25 | `C078` | code-review (both lenses) | lib/services/executors/claude-agent/{clirunner_test.go,clistreamparser.go,clistreampars… | DONE |  |
| 7 | 25 | `C079` | code-review (both lenses) | lib/services/executors/claude-agent/{requestparse.go,schema.go,schema_test.go,server.go… | DONE |  |
| 7 | 25 | `C080` | code-review (both lenses) | lib/services/executors/claude-agent/cmd, lib/services/executors/http-node, lib/services… | DONE |  |
| 7 | 25 | `C081` | code-review (both lenses) | lib/services/executors/verifier-http, lib/services/executors/verifier-http/cmd, lib/ser… | DONE |  |
| 7 | 26 | `C082` | code-review (both lenses) | lib/services/sensors/sensor-cron, lib/services/sensors/sensor-http | DONE |  |
| 7 | 26 | `C083` | code-review (both lenses) | lib/services/sensors/sensor-object-store, lib/services/sensors/sensor-webhook | DONE |  |
| 7 | 26 | `C084` | code-review (both lenses) | lib/services/subscribers/openlineage | DONE |  |
| 7 | 26 | `C085` | code-review (both lenses) | lib/services/test/harness, lib/services/test/overlapproducer | DONE |  |
| 7 | 26 | `C086` | code-review (both lenses) | lib/services/test/scenarios/{bundled_inproc_dispatch_test.go,cascade_send_demo_e2e_test… | DONE |  |
| 7 | 27 | `C087` | code-review (both lenses) | lib/services/test/scenarios/{control_api_compose_prefix_guard_e2e_test.go,control_api_i… | DONE |  |
| 7 | 27 | `C088` | code-review (both lenses) | lib/services/test/scenarios/{sensor_cron_restart_recovery_e2e_test.go,sensor_http_e2e_t… | DONE |  |
| 7 | 27 | `C089` | code-review (both lenses) | lib/services/test/scenarios/{verifier_severity_partition_e2e_test.go} | DONE |  |
| 7 | 27 | `C090` | code-review (both lenses) | lib/services/test/scenarios/atomic_staging, lib/services/test/scenarios/claim_producers… | DONE |  |
| 7 | 27 | `C091` | code-review (both lenses) | lib/services/test/smoke, lib/services/test/stubexecutor, test/plumbline | DONE |  |
| 7 | 28 | `C092` | code-review (both lenses) | test/scenarios/{acquire_pass_invalidate_emit_test.go,acquire_unavailable_abandon_inject… | DONE |  |
| 7 | 28 | `C093` | code-review (both lenses) | test/scenarios/{cascade_two_node_backedge_in_frame_test.go,cascade_two_node_backedge_vi… | DONE |  |
| 7 | 28 | `C094` | code-review (both lenses) | test/scenarios/{fanout_callback_determinism_e2e_test.go,fanout_child_error_retry_e2e_te… | DONE |  |
| 7 | 28 | `C095` | code-review (both lenses) | test/scenarios/{host_agent_latebind_all_protocols_test.go,host_agent_per_binding_exec_o… | DONE |  |
| 7 | 28 | `C096` | code-review (both lenses) | test/scenarios/{lineage_exploration_e2e_test.go,loop_counter_cap_e2e_test.go,message_qu… | DONE |  |
| 8 | 29 | `C097` | code-review (both lenses) | test/scenarios/{peer_tls_test.go,producer_class_routing_test.go,pure_cascade_test.go,re… | DONE | folded |
| 8 | 29 | `C098` | code-review (both lenses) | test/scenarios/{story_debug_channel_e2e_test.go,story_message_schema_e2e_test.go,story_… | DONE | folded |
| 8 | 29 | `C099` | code-review (both lenses) | test/scenarios/{template_fan_out_e2e_test.go,template_lifecycle_e2e_test.go,template_su… | DONE | folded |
| 8 | 29 | `C100` | code-review (both lenses) | test/scenarios/asset, test/scenarios/asset_management, test/scenarios/attributes | DONE | folded |
| 8 | 29 | `C101` | code-review (both lenses) | test/scenarios/auth | DONE | folded |
| 8 | 30 | `C102` | code-review (both lenses) | test/scenarios/breakpoints, test/scenarios/canary, test/scenarios/claim_handle_aggregate | DONE | folded |
| 8 | 30 | `C103` | code-review (both lenses) | test/scenarios/claim_producers, test/scenarios/empty_message_wake, test/scenarios/fanou… | DONE | folded |
| 8 | 30 | `C104` | code-review (both lenses) | test/scenarios/frame_resolution, test/scenarios/instance_create_is_idle, test/scenarios… | DONE | folded |
| 8 | 30 | `C105` | code-review (both lenses) | test/scenarios/lineage, test/scenarios/locks, test/scenarios/messages, test/scenarios/p… | DONE | folded |
| 8 | 30 | `C106` | code-review (both lenses) | test/scenarios/run_tree, test/scenarios/sensor, test/scenarios/subgraph, test/scenarios… | DONE | folded |
| 8 | 31 | `C107` | code-review (both lenses) | test/support/claim_producers/stub/dataprocessing, test/support/claim_producers/stub/lif… | DONE | folded |
| 8 | 31 | `C108` | code-review (both lenses) | test/support/pgmigrate, test/support/scenario, test/support/testpg | DONE | folded |
| 8 | 31 | `C109` | code-review (both lenses) | tools/license-check, tools/rulesdoc | DONE | folded |
| 8 | 31 | `D00` | decision-doc | decisions/: acquire-prefix-fallback, acquire-unavailable-carveout, advisory-locks, allo… | DONE | folded |
| 8 | 31 | `D01` | decision-doc | decisions/: async-callback-persistent-registry, async-callback-post-json, attribute-car… | DONE | folded |
| 8 | 32 | `D02` | decision-doc | decisions/: auth-grant-scope, blessed-invariant-annotations, blob-backend, blob-backend… | DONE | folded |
| 8 | 32 | `D03` | decision-doc | decisions/: bundled-executor-inproc-capability-advertisement, bundled-registry-entrypoi… | DONE | folded |
| 8 | 32 | `D04` | decision-doc | decisions/: claude-agent-attribute-only-session, claude-agent-cli-expose-env-field, cla… | DONE | folded |
| 8 | 32 | `D05` | decision-doc | decisions/: comment-drift-sweep, comment-hygiene-uniform-rule, compose-driver-sends-emp… | DONE | folded |
| 8 | 32 | `D06` | decision-doc | decisions/: cron-robfig-v3, debug-channel-gate-paused-or-breakpoint, depguard-consumpti… | DONE | folded |
| 9 | 33 | `D07` | decision-doc | decisions/: depguard-runtime-purity, design-link-annotations, doc-residue-reshape-pass,… | DONE | folded |
| 9 | 33 | `D08` | decision-doc | decisions/: envelope-type-discriminator, event-log-kind-enum, event-log-payload-shapes,… | DONE | folded |
| 9 | 33 | `D09` | decision-doc | decisions/: force-upstream-refresh-via-receiver-keyed-map, frame-isolation-is-structura… | DONE | folded |
| 9 | 33 | `D10` | decision-doc | decisions/: hard-dep-settled-guard, harness-first-ordering, held-as-state-not-phase, ht… | DONE | folded |
| 9 | 33 | `D11` | decision-doc | decisions/: image-set-bundled-services, image-set-four-core, image-tagging-version-and-… | DONE | folded |
| 9 | 34 | `D12` | decision-doc | decisions/: inproc-registry, inproc-transport-client, internal-mcp-server-net-http, jcs… | DONE | folded |
| 9 | 34 | `D13` | decision-doc | decisions/: launch-integration, layer-ordering, licensing-dual-apache-agpl, licensing-e… | DONE | folded |
| 9 | 34 | `D14` | decision-doc | decisions/: merge-validator-warnings, message-idempotencies-dedup-tuple, message-queue-… | DONE | folded |
| 9 | 34 | `D15` | decision-doc | decisions/: mode-default-most-recent, module-split, named-lock-metric, network-binding,… | DONE | folded |
| 9 | 34 | `D16` | decision-doc | decisions/: node-state-retired-from-operator-api, non-cascade-direct-to-stale, one-mess… | DONE | folded |
| 9 | 35 | `D17` | decision-doc | decisions/: peer-tls-enforcement, per-service-load-opts-from-env, persistence-driver, p… | DONE | folded |
| 9 | 35 | `D18` | decision-doc | decisions/: pre-v1-break-freely, pre-v1-pure-removal-for-retired-surfaces, prior-stale-… | DONE | folded |
| 9 | 35 | `D19` | decision-doc | decisions/: project-agnostic, protocol-version-v1-namespaced, protojson-gateway, race-g… | DONE | folded |
| 9 | 35 | `D20` | decision-doc | decisions/: release-dev-mechanical, release-distribution, release-formal-skill, release… | DONE | folded |
| 9 | 35 | `D21` | decision-doc | decisions/: rimsky-compose-run-scope, rimsky-run-self-hosts-templates, run-name, run-sc… | DONE | folded |
| 9 | 36 | `D22` | decision-doc | decisions/: sequence-scope-monotonic, service-spawn-flag, services-source, signoff-cryp… | DONE | folded |
| 9 | 36 | `D23` | decision-doc | decisions/: sqlite-multiproc-safety, structural-root-edge-injection-at-registration, su… | DONE | folded |
| 9 | 36 | `D24` | decision-doc | decisions/: substitution-grammar-closed, substitution-grammar-fallback-routing, substit… | DONE | folded |
| 9 | 36 | `D25` | decision-doc | decisions/: termination, test-harness-create-instance-wakes-roots-after-create, test-ha… | DONE | folded |
| 9 | 36 | `D26` | decision-doc | decisions/: tls-mode-validation, toplevel-dirs, topology-test-coverage, typescript-clau… | DONE | folded |
| 10 | 37 | `D27` | decision-doc | decisions/: uuid-google, validation-error-names-mode, validation-errors-additive-not-un… | DONE | folded |
| 10 | 37 | `S00` | story-doc | stories/: all-upstream-gating, anonymous-mode-bootstrap, api-key-management, asset-mana… | DONE | folded |
| 10 | 37 | `S01` | story-doc | stories/: cascade-defers-during-flight, cascade-send, cascade-signal-blind, claim-hando… | DONE | folded |
| 10 | 37 | `S02` | story-doc | stories/: claim-producer-scopes-conflict, claim-scope-substitution, claude-agent-expose… | DONE | folded |
| 10 | 37 | `S03` | story-doc | stories/: commit-response-honored, compose-lifecycle, compose-namespace-guard, data-pro… | DONE | folded |
| 10 | 38 | `S04` | story-doc | stories/: event-log-read, executor-protocol, executor-reads-dispatch-context, executor-… | DONE | folded |
| 10 | 38 | `S05` | story-doc | stories/: fs-fanout-expand-folder, fs-fanout-list-array, grant-scope-enforcement, held-… | DONE | folded |
| 10 | 38 | `S06` | story-doc | stories/: host-agent-per-binding-overrides, host-agent-per-run-scope-isolation, http-no… | DONE | folded |
| 10 | 38 | `S07` | story-doc | stories/: lenient-marker, lifecycle-subscriber-author, lineage-admin, lineage-explorati… | DONE | folded |
| 10 | 38 | `S08` | story-doc | stories/: mcp-transport, message-bus, message-queue-coalesces-pending, message-schema, … | DONE | folded |
| 10 | 39 | `S09` | story-doc | stories/: one-message-per-frame, one-shot-to-terminal, opaque-executor-scratch, operato… | DONE | folded |
| 10 | 39 | `S10` | story-doc | stories/: producer-class-routing, producer-error-passthrough, publisher-protocol, ref-v… | DONE | folded |
| 10 | 39 | `S11` | story-doc | stories/: runtime-diagnostics, script-friendly-outcome, sensor-cron, sensor-http, senso… | DONE | folded |
| 10 | 39 | `S12` | story-doc | stories/: spawned-local-services, store-filesystem, store-postgres, sub-claim-payload-s… | DONE | folded |
| 10 | 39 | `S13` | story-doc | stories/: template-error-policy, template-fan-out, template-lifecycle, template-sub-gra… | DONE | folded |
| 10 | 40 | `S14` | story-doc | stories/: upstream-pull-on-invalidate, validation-author, validation-mixin-uniform, val… | DONE | folded |
| 10 | 40 | `T00` | tension-validity | tensions/: blessed-invariant-14-retired, blob-backend-conformance-fixture-asymmetry, ca… | DONE | folded |
| 10 | 40 | `T01` | tension-validity | tensions/: memory-blob-audit-gap, per-cascade-source-mode, pre-v1-hash-instability, qua… | DONE | folded |
| 10 | 40 | `T02` | tension-validity | tensions/: stub-mode-runtime-only-gate, stub-mode-signature-no-proto-surface, substitut… | DONE | folded |
