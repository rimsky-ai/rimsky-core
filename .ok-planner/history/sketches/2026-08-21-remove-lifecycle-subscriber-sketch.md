# Remove the lifecycle-subscriber protocol — Design Sketch

**Date:** 2026-08-21
**Status:** Abandoned 2026-08-22. Discussion found the protocol is sound and only run-scope terminal bypasses the outbox; superseded by `sketches/2026-08-22-lifecycle-run-scope-terminal-on-outbox-sketch.md`.

## Idea

Remove the lifecycle-subscriber protocol from rimsky: the proto, the fan-out, the outbox and idempotency ledger, the termination poll loop, the conformance runner, the validation role, the config key, and the corpus artifacts that define it. No bundled service consumes the protocol. The two bundled claim producers register a stub that acks every callback and does nothing (`lib/services/claim_producers/shared/lifecycle/lifecycle.go`). The openlineage subscriber polls the durable lineage projection instead of subscribing (`decision:lineage-subscriber-poller`). Every transition the protocol carries already lands in `concept:event-log`, which is durable and restart-safe. The protocol's one real consumer is the host-agent proxy, which uses it to fill its per-instance binding cache and to reap late-bound spawns at a run-scope terminal. The sketch moves both onto the control API, which the proxy already dials.

## Shape

### What goes

Code:

- `lib/protocols/proto/v1/lifecycle.proto` and its generated types; `lib/protocols/lifecycle`; `lib/protocols/serverkit/bridge.go::MountLifecycle`.
- `lib/foundation/lifecycle` (the subscriber registry); `lib/runtime/peer/lifecycle_client.go`; `lib/control/config::DialLifecycleSubscribers` and the registry and reconciler fields on the control-api handle.
- `lib/control/controlapi/lifecycle.go` whole: the fan-out functions, `LifecycleReconciler`, `LifecyclePeersForSpec`, the outbox and idempotency writes; every call site in template, instance, and run-scope transitions.
- Tables `rimsky_lifecycle_outbox` and `rimsky_lifecycle_idempotencies`, dropped by a new numbered migration in both backends; `cfg:claim_producers[].lifecycle_outbox_trailing`.
- `lib/protocols/conformance/lifecyclesubscriber` and the `rimsky conformance lifecycle-subscriber` subcommand; the `lifecycle_subscriber` role in `rimsky conformance validation --role` and in `lib/runtime/validation_pipeline.go`.
- `lib/services/claim_producers/shared/lifecycle` and the registrations in the filesystem and postgres producers; `test/support/claim_producers/stub/lifecycle` and its registration.
- `cmd/rimsky-host-agent-proxy/lifecycle_handler.go`, replaced as below.
- Every event kind emitted only by the removed code (`lifecycle_reconciler.*`, the fan-out failure kinds).
- Persistence surface beyond the two tables: `lib/foundation/persistence/lifecycle_outbox.go` and `lifecycle_idempotency.go` with their sqlite and postgres implementations; `Tables.LifecycleIdempotency()` and `Tables.LifecycleOutbox()` (`lib/foundation/persistence/tables.go`); `AdvisoryLocker.TakeLifecycleScopeLock` (`lib/foundation/persistence/database.go`, both backends); `InstanceTable.ListTerminatedWithLifecycleRows` (`lib/foundation/persistence/instances.go`), which exists only for the termination poll; the idempotency-row purge inside instance hard-delete and its test `TestDeleteInstance_PurgesRunScopeLifecycleIdempotencyRows`.
- The `lifecycle_subscriber` protocol name in the service manifest vocabulary (`cmd/rimsky/cli/compose/manifest.go`, tested at `manifest_test.go`), so a compose manifest declaring it fails validation instead of being accepted and ignored.
- Test support: the scenario harness's `LifecyclePeersForSpec` wiring (`test/support/scenario/harness.go`); the fitness pins that name the protocol (`test/plumbline/module_layout_test.go` proto list, `test/plumbline/cli_api_key_universal_test.go` conformance-verb entry).
- Migration 037 renamed a column on the idempotency table; the drop migration supersedes it and 037 stays as history.

Corpus:

- Retire `concept:lifecycle-subscriber`, `story:lifecycle-subscriber-author`, `decision:lifecycle-subscriber-at-least-once-delivery`, `decision:lifecycle-fanout-after-commit`.
- Amend `concept:service`, `concept:claim-producer`, `concept:executor`, `concept:publisher`: delete the opt-in sentence.
- Amend `story:validation-author`: three role contexts, not four.
- Amend `concept:host-agent-proxy`: the cache fills from the control API on a miss and evicts on a terminal state the control API reports.
- Amend `decision:lineage-subscriber-poller`: the rejected alternative no longer exists; the choice stands on durability alone.
- Issues: retire `issue:lifecycle-outbox-retention-narrows-at-least-once`, `issue:instance-delete-drops-undelivered-lifecycle-events`, `issue:event-log-domain-for-peer-delivery-health`. Amend `issue:peer-readiness-gate-is-generic` to drop subscribers from its peer population and its "instance creation as well as deploy" paragraph.

Docs: `docs/examples/lifecycle-subscriber-author.md` goes; `docs/grpc.md`, `docs/protocols/*.md`, `docs/cli.md`, `docs/config.md`, `docs/concepts.md`, `docs/capabilities.md`, `docs/templates.md`, `docs/operating.md`, `docs/images.md`, `README.md`, and the host-agent examples lose their references. The `/document` run at the next release revises them; the sprint scrubs the protocol tables and the conformance list by hand so the tree does not ship a doc for a protocol that is gone.

### What replaces the proxy's two uses

The proxy today does two things on lifecycle events (`cmd/rimsky-host-agent-proxy/lifecycle_handler.go`):

1. `OnInstanceCreated` fills the per-instance cache (service bindings, target routing identity, params); `OnInstanceTerminated` drops it.
2. `OnRunScopeTerminal` drops the spawns held for that run scope and sends each agent a `Reap` frame.

Cache: the miss path already exists — `cmd/rimsky-host-agent-proxy/dispatch.go::newControlAPIFetcher` calls `route:GET /v1/instances/{id}`, which returns `service_bindings`, `target_routing_identity`, and `state`. It becomes the only fill. Eviction: a cached entry carries a TTL; a dispatch that arrives after the TTL re-fetches, and a fetch returning 404 or a terminal `state` drops the entry. Because a terminated instance never dispatches again, a stale entry costs memory only, and the TTL bounds that.

Reap: the proxy keeps a small set of live spawns, each keyed by run scope. It runs one poll loop over the distinct instances in that set, on an interval (`env:RIMSKY_PROXY_SPAWN_POLL_INTERVAL`, default on the order of the current termination poll), calling the same instance GET. An instance in a terminal state reaps every spawn held under it, with the existing `Reap` frame and grace. A spawn is also reaped when its agent disconnects, as today.

```
dispatch(instance X) ──miss──▶ GET /v1/instances/X ──▶ cache[X] (ttl)
                                                    └─▶ spawn under X recorded

poll tick ─▶ for each instance with live spawns:
               GET /v1/instances/X
               state terminal or 404 ─▶ drop cache[X], reap spawns under X
```

### Sequencing inside one sprint

The change is one sweep, not a deprecation: the proto deletion breaks every consumer at compile time and the compiler enumerates the blast radius. Order the stages so the tree builds at each boundary: proxy replacement first (it still compiles against the old proto), then the control-api fan-out and registry, then the proto and the protocol packages, then the migration, then services and test support, then corpus and docs.

## Open questions

- **Run-scope granularity of the reap.** Today a sub-graph run-scope terminal reaps that scope's spawns while the instance lives. The instance GET reports instance state only. Either accept instance-terminal reaping (spawns outlive their sub-graph until the instance ends or the agent disconnects) or add run-scope state to the instance response or a `route:GET /v1/run-scopes/{id}`. The sketch assumes instance-terminal reaping is enough; a long-lived instance with many short sub-graph late-binds would say otherwise.
- **Poll interval and who owns it.** The proxy gains a clock-driven loop. Per the testing standard the tick must be injectable; the sketch assumes the proxy already carries a `Clock`.
- **Anonymous mode.** The proxy's control-API token may be absent. The instance GET already works on the cache-miss path under every posture the proxy supports, so the sketch assumes the poll inherits that; confirm the anonymous posture permits the GET for an instance the caller did not create.
- **Does anything outside the tree subscribe?** The sketch assumes no external consumer; the protocol was public per `docs/protocols/`. Removal is a breaking change, legal pre-v1 (`.claude/rules/rules.md`).
- **`validation-author` role contexts.** Removing the fourth role touches the validation request proto's role enum. The sketch assumes renumbering is acceptable pre-v1; otherwise reserve the value.

## Risks / unknowns

- The fan-out is threaded through every template, instance, and run-scope transition in `lib/control/controlapi`; pulling it out touches the transaction boundaries those transitions use (`withLifecycleScopeTx`, `withOptionalTx`). The removal must leave each transition's own commit unchanged.
- Scenario tests under `test/scenarios/` that wait on lifecycle delivery (`instance_lifecycle_fullstack_test.go`, `parked_lifecycle_test.go`, the host-agent isolation tests) need their waits re-pointed at event-log kinds or at the proxy's own events.
- The proxy's reap becomes bounded by a poll interval instead of a push; a test that waited on the `Reap` frame after a run-scope terminal must fire the tick.
- `/events` will show kinds that become orphans; the sprint prunes them in the same change.

## What this is not

- Not a redesign of peer readiness gating. `issue:peer-readiness-gate-is-generic` stays open for executors, claim producers, sensors, and publishers; only subscribers leave its population.
- Not a change to `concept:event-log` or `concept:signal`. Nothing new is emitted; consumers that need control-plane history read what is already recorded.
- Not a replacement push channel. A peer that wants to be told, rather than to read, has no rimsky mechanism after this; that is the intended end state.
