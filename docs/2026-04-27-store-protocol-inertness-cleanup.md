# Store Protocol Inertness Cleanup — Notes

**Date:** 2026-04-27
**Status:** notes only; informal design doc to be turned into a spec via the standard brainstorm/spec/plan flow before execution.

## Why this exists

stores-redesign-v2 (`docs/specs/2026-04-27-stores-redesign-v2-design.md`) landed the 5-verb protocol and the two-noun primitives split. Post-implementation review surfaced several places where rimsky reaches *inside* a store substrate's internal state via non-protocol surfaces (type assertions, substrate-only accessor methods, substrate-aware SQL). They slipped through the v2 design discussions and need to come out before the next major push (stores out-of-process), where every rimsky↔store interaction must be expressible over the 5-verb protocol.

This doc captures the cleanup goals so we don't lose them. It is intentionally informal — the brainstorm will refine the principle, resolve the open questions, and produce a proper spec.

## Principle (draft — to refine in brainstorm)

1. **Protocol-only.** Rimsky talks to stores exclusively through the 5 protocol verbs. Type assertions to a concrete store type in any rimsky package outside the substrate's own package are forbidden.
2. **Opaque selectors.** Selectors are strings rimsky carries unchanged from template DSL through to substrate verbs. Rimsky does not parse, classify, or look up selectors against substrate state.
3. **Substrate-internal capabilities are substrate-internal.** Queue maintenance, items tables, pick policies, sweeps, item seeding — all the substrate's own. A store substrate that wants to expose item-management endpoints does so on its own service surface.

## Concrete violations to remove

1. **Admin items endpoint** — `POST /admin/stores/{name}/pick-policies/{selector}/items` in `core/controlapi/admin_claim_stores.go`. Type-asserts `*pgstore.Store` and calls a non-protocol method.
2. **Substrate-only methods on `*pgstore.Store`** — `InsertItems`, `PickPolicyConfig`, `PickPolicies`. Exist solely to feed (1), (3), (4).
3. **Pick-policy validator hook** — `RegistryHooks.IsPickPolicySelector` in `core/node/template_validator.go` plus `validatorHooksFor` in `core/controlapi/templates.go`. Rimsky inspects substrate state to enforce `intent: rw` on pick-policy selectors.
4. **Scheduler visibility-timeout sweep** — `core/scheduler/sweep_locks.go` walks each postgres store's pick policies and runs SQL against operator-owned items tables.

## Doc / spec surfaces affected

- CLAUDE.md — bullets that reference the admin URL, the §12.12 sweep, and the `IsPickPolicySelector` validation.
- `docs/operator-guide.md` §5.5 — admin items endpoint section.
- `docs/operator-guide.md` §3.4.3 — items-table schema documentation; needs reframing as substrate-internal rather than rimsky-contract.
- `docs/specs/2026-04-27-stores-redesign-v2-design.md` — **do not retroactively edit.** It is the historical record of what shipped. The brainstorm/spec from this doc supersedes the relevant sections.

## What stays (draft — to confirm in brainstorm)

- The 5-verb protocol unchanged.
- The `pick_policies:` block in `stores.yml` as substrate-opaque substrate-defined config; the postgres factory parses its own keys, rimsky's `Registry.BuildAll` is otherwise blind.
- Rimsky's own control-plane DB and `RIMSKY_DB_URL` are unaffected — the principle applies to *stores*, not to rimsky's own state machine.

## Open questions for the brainstorm

These came up while drafting the cleanup goals; flagging here so the brainstorm doesn't miss them. None of them are decided.

1. **Principle framing — does the three-rule formulation above match the architectural intent?** Specifically the "what stays" carve-out for `pick_policies` in stores.yml as substrate-opaque config. Could push harder: in the out-of-process world, even the substrate-config block disappears from rimsky's view (rimsky just has a service endpoint). Whether the in-process artifact is acceptable as a transitional state, or whether stores.yml should already pretend the substrate is opaque, is a real call.

2. **Smoke fixture item-seeding.** Once the admin endpoint is gone, the smoke test has to seed items some other way. Options: (a) direct SQL against `pool` from the test fixture — works today but is itself a soft violation of the same principle (the test reaches into substrate internals); (b) the in-process postgres store grows a fixture-scoped seam (still reaches into substrate but through a sanctioned test path); (c) wait for the out-of-process work and have the store-service expose an admin endpoint the smoke calls. Picking (a) for now is cheap; the brainstorm should decide whether that's acceptable test scaffolding or whether the principle applies to tests too.

3. **Visibility-timeout sweep — operational hole.** Deleting it leaves a real gap until either the in-process postgres store grows its own goroutine or the out-of-process work lands. The remaining backstop is the heartbeat-driven release path on `rimsky_lock_holders` (when a supervisor's heartbeat lapses, the orphan reap deletes the lock-holder row, CASCADE-deletes claim-holder rows, and the substrate sees stale items on its next `Open`). This is materially weaker than a periodic sweep over the items table itself. The brainstorm needs to decide: is this acceptable for the demo stage, or do we want a substrate-side internal sweep before stripping the rimsky-side one?

4. **Action vocabulary on `claim_resolutions`.** The strings `delete` / `release_to_back` / `release_to_head` flow through the 5-verb protocol as opaque per-call action payloads, so rimsky's *runtime* surface treats them correctly. But rimsky's *template DSL grammar* enumerates them, which means a non-queue substrate would see template-DSL hints about substrate-side actions it doesn't recognize. The brainstorm should decide whether this is in scope for the cleanup (push the action vocabulary off the template DSL into a substrate-defined token) or out of scope (template DSL stays as-is; substrates that don't honor `release_to_back` etc. just ignore the strings).

## Operational impact (informational; subject to brainstorm)

- Demo / dev deployments lose the admin items endpoint. Operators that need to seed items use direct SQL. Migration: external tooling that posted to `/admin/stores/.../items` breaks.
- Smoke fixture switches to whichever option the brainstorm picks for question 2.
- Visibility-timeout backstop weakens until question 3 is resolved.

## Scenario test coverage gap to restore after cleanup

The v2 cutover deleted ~38 test files across `test/scenarios/{locks,stores,attributes,claim_stores}/` and replaced them with one-line placeholder packages. Cycle-1 review added back six substantive tests; the rest is owed. The cleanup goes first because it modifies surfaces the missing tests would otherwise cover, so writing them pre-cleanup means rewriting them post-cleanup.

After the cleanup spec lands, the scenario suite needs:

**Already added (cycle-1) — keep:**
- `test/scenarios/locks/atomic_acquisition_test.go` — invariants 3, 10
- `test/scenarios/stores/regional_claim_test.go` — 5-verb path end-to-end on regional access
- `test/scenarios/attributes/substitution_dispatch_test.go` — invariant 12
- `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go` — invariant 13
- `core/store/postgres/store_test.go` — substrate-internal Open/Commit/Abandon
- `core/supervisor/auto_terminal_test.go` — `CheckAndFireResolution` aggregate paths

**Owed (plan T31-T41) — invariant-bound, write after cleanup:**
- `verify_open_inside_acquisition_tx_test.go` — invariant 15 (Open fires inside the acquisition tx; substrate-side state mutations and the lock-holder row's address update participate in the same tx)
- `inertness_audit_test.go` — invariant 20 behavioral coverage (claim content never leaks into logs / events / errors)
- `single_writer_per_region_test.go` — invariant 9b (no per-store reader-lease serialization)

**Owed — behavioral / propagation:**
- `auto_terminal_failure_propagation_test.go` — `on_give_up` propagation when a sibling fails in a holding subgraph
- `address_inheritance_lifetime_test.go` — claim-address visibility across a holding subgraph (inheritor sees the acquirer's address bytes)
- `value_pass_lifetime_test.go` — value-pass vs claim-pass propagation modes

**Owed — documentation / contract:**
- `frame_id_observability_only_test.go` — frame_id round-trips through `rimsky_lock_holders` / `rimsky_claim_holders` (observability, not behavioral)
- `staged_async_protocol_present_no_substrate_test.go` — protocol-honest failure when staged_async is configured but the substrate doesn't honor it

**Will change shape because of the cleanup — write against post-cleanup contracts:**
- `pick_policy_selector_test.go` — original plan called for testing rimsky's pick-policy dispatch; post-cleanup, rimsky doesn't dispatch on selector shape (selectors are opaque). The test that survives is "selectors flow through Open verbatim" — a substrate-opacity test, not a pick-policy test.

**Pre-v2 deletions to rewrite against the new harness:**
- locks/: named-lock counting, named-lock contention, sorted acquisition order (no deadlock), claimant-guarded release, region-conflict, orphan reap.
- stores/: filesystem-direct write/read, disjoint vs. overlapping regions, single-writer-per-region (covered above).
- attributes/: substitution from deps/claim/params, schema validation, incremental + terminal-final writeback, userdata opacity, value-pass vs. claim-pass lifetime.
- claim_stores/: on-commit / on-give-up actions, multi-claim subgraphs, auto-terminal aggregate outcome (covered above).

Sequencing: invariant-bound tests are the highest-priority follow-up because they protect blessed properties that are easy to regress in any future refactor. Behavioral / propagation tests are next. Pre-v2 deletions can land last (the unit-level coverage of the underlying packages is solid; scenario rewrites are belt-and-suspenders).

## Deployment-stack verification owed

Two verification layers were skipped during the v2 cutover and are still owed. They should run *after* the cleanup, not before — the cleanup modifies the deployment surface (drops the admin endpoint, drops the visibility-timeout sweep, updates CLAUDE.md / operator-guide), so running these now would mostly validate a transient state.

**T55 — Docker smoke.** Bring up `deploy/docker-compose.yml` and verify the stack reaches `/health`. Catches everything the in-process smoke fixture cannot:
- gRPC dial across containers (executor ↔ supervisor)
- Async-callback HTTP dial-back across containers (`RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` plumbing — the listener binds `0.0.0.0` but executors need a peer-reachable hostname)
- Env-var ingestion in container env vs. local-shell env
- Postgres connectivity via service-name DNS (`postgres:5432`)
- Migration runner against a fresh `postgres:15` container, including session advisory lock + idempotency
- Stale env-var names in the compose file (CLAUDE.md flags the Helm chart as known-stale; compose is similar territory)
- The init-items one-shot creating the items table out-of-band (this becomes nuanced after the cleanup — see §3.5: items tables reframe as substrate-internal documentation, but the init-items service itself stays because operators still need a path to create the table)

The compose file uses `image: rimsky/<service>:latest` references (not inline `build:` directives), so `deploy/build-images.sh` must run first to populate the local Docker daemon's image store. No registry push/pull is required — image distribution is out of scope for docker smoke.

Mechanical sequence:
```
deploy/build-images.sh
docker compose -f deploy/docker-compose.yml up -d
curl http://localhost:8080/health
# exercise force-fire / template-deploy paths against the live stack
docker compose -f deploy/docker-compose.yml down -v
```

**T56 — Conformance.** Run `rimsky-conformance` against the live http-node and claude-agent containers brought up by docker smoke. Catches:
- Whether each executor honors every required RPC in the protocol
- Whether the async-callback POST shape matches the supervisor's chi route exactly (CLAUDE.md gotcha: body keyed `type` not `kind`; route is `/v1/callback/{async_ack_id}`)
- Whether stub mode actually short-circuits LLM calls (`--require-stub-mode` issues a probe via `rimsky-conformance-probe` at startup)

Mechanical sequence (after T55 stack is up):
```
go run ./core/cmd/rimsky-conformance --endpoint http://localhost:9091 --transport grpc --require-stub-mode
go run ./core/cmd/rimsky-conformance --endpoint http://localhost:9090 --transport grpc --require-stub-mode
```

Both checks should land in the cleanup's final-verification phase. Sequence: cleanup → unit + scenario + smoke (in-process) green → docker smoke → conformance → done.

## Next step

Brainstorm session to refine the principle, resolve the four open questions, then formalize as a spec under `docs/specs/` and an executable plan under `docs/plans/`. Scenario coverage restoration and deployment-stack verification both follow the cleanup as final phases of the cleanup plan (or as separate plans — the brainstorm decides).
