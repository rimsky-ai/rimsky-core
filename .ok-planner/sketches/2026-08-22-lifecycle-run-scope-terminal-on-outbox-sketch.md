# Lifecycle delivery through one outbox — Design Sketch

**Date:** 2026-08-22
**Status:** Sketch (not a sprint; not authorization to build)

## Idea

Keep the lifecycle-subscriber protocol and deliver every one of its seven events through the lifecycle outbox. Today six events are staged in the transaction that performs the transition and drained by the reconciler; run-scope terminal is not. The frame engine calls each peer directly when a frame settles (`lib/runtime/lifecycle_fanout.go`), records success in the idempotency ledger, and never retries a failure except through the terminated-instance drain, which only covers scopes whose instance has ended. Instance terminated is staged nowhere: the terminate route closes the instance's run scopes and calls peers directly for each, and the reconciler's poll loop delivers the instance event later by scanning terminated instances that still hold a ledger row. The sketch stages both from their transitions, moves the staging primitive below `lib/control` so the frame engine can reach it, and drops the idempotency ledger, because after the change every job it does is a job the outbox row already does. The host-agent proxy, the protocol's one real consumer, keeps reaping late-bound spawns on `OnRunScopeTerminal`; it gains a retry it never had.

This supersedes `history/sketches/2026-08-21-remove-lifecycle-subscriber-sketch.md`, which proposed removing the protocol and moving the proxy onto a control-API poll. Discussion found the protocol sound and the direct call the only defect.

## Shape

### Staging moves to foundation

`stageLifecycleDeliveries` and the two typed wrappers (`StageTemplateLifecycleEvent`, `StageInstanceLifecycleEvent`) leave `lib/control/controlapi/lifecycle_outbox.go` for a package beside the table, `lib/foundation/lifecycle` or `lib/foundation/persistence`. The `LifecycleEvent` enum, the staged payload struct, and `LifecyclePeersForSpec` (the peer list a template spec names) move with them. The six control-layer call sites in `templates.go` and `instances.go` import from the new home and change nothing else. The `runtime-purity` and `foundation-purity` depguard rules already permit the direction.

A third wrapper, `StageRunScopeLifecycleEvent`, stages one row per peer for a closed scope with the payload `{run_scope_id, instance_id, terminal_reason}`.

### Run-scope terminal is staged at frame end

`transitionFrameEnd` (`lib/graph/frame/engine.go`) already closes the scope tree and returns the settled scope ids before the commit. It stages the run-scope rows inside the frame-end transaction, one per (peer, scope), in the order `closeSettledFrameScopeTree` returns them: children before parents. The `scopeFanout` callback, `FrameRunScopeTerminalFanout`, and the scheduler's `LifecycleSubs` and `LifecyclePeersForSpec` config fields go. The frame engine needs the peer list for the instance's template; it reads the spec it already has in the frame-end path, or the staging wrapper takes the instance id and resolves the spec itself.

Delivery does not wait for the reconciler's interval. The reconciler gains a `Kick()`, as the producer-verb dispatcher has (`lib/runtime/producer_verb_outbox.go::kickProducerVerbDispatch`): a channel send that wakes the drain at once. The frame engine calls it after the frame-end transaction commits, through a post-commit hook the scheduler wires into `frame.RunTick` in place of the removed `scopeFanout`. The drain then delivers the new rows on the same path the interval uses. The interval (2s default) becomes retry-only, which is what it already is for the six control-route events, whose routes deliver inline after commit. The sketch keeps the peer RPC off the scheduler tick: a kick is one channel send, where an inline delivery from the frame engine would hold every other sweep behind a peer's response time.

The kick crosses the layer line in the other direction from the staging move: the reconciler lives in `lib/control`, and the scheduler in `lib/runtime` may not import it. The scheduler takes a `LifecycleKick func()` config field, as the callback server takes `ProducerVerbKick`, and `lib/control/config` wires the reconciler's `Kick` into it at assembly.

### Instance terminated is staged at the transition

The terminate route and the delete route each stage `instance_terminated` in the transaction that stamps `terminated_at`, after staging run-scope terminal for each scope the termination closes, and deliver inline after commit through `deliverStagedLifecycleAfterCommit` as the other routes do. The stream key is (peer, instance scope), and run-scope rows use the run-scope scope kind, so a peer receives the scope closures and the instance closure in two streams. Rimsky promises no ordering across streams today, and this sketch adds none; the proxy does not depend on it, since `OnInstanceTerminated` only drops a cache entry.

The reconciler's `drainTerminatedInstances` pass, `ListTerminatedWithLifecycleRows`, `CloseAndFanOutRunScopesForInstance`, and `fanOutInstanceTerminatedFromLifecycleRows` go. The reconciler becomes the staged drain alone.

### The idempotency ledger goes

A new numbered migration in both backends drops `rimsky_lifecycle_idempotencies`; the sweep also removes `LifecycleIdempotencyTable`, `Tables.LifecycleIdempotency()`, the state enum, and the purge inside instance hard-delete. Each reader gets a replacement:

| Reader | Today | After |
| --- | --- | --- |
| staged drain skips a delivery the peer already acked | ledger state equals target | gone: a row is deleted in the same transaction as the ack; a replay after a crash between ack and delete is the at-least-once duplicate the decision already accepts |
| staged drain skips a closing event for a scope the peer never heard open | no ledger row | gone: the opening row is ahead of it in the same stream, and the stream delivers in order |
| run-scope terminal dedups itself | ledger state is `run_scope_terminal` | gone: the row is the record |
| terminated-instance drain finds its work | terminated instances with a ledger row | gone: the terminate route stages the row |
| `GET /v1/observability/peers` reports delivery state | ledger rows per peer | the peer's pending outbox rows: count, oldest `staged_at`, and the events by scope |

The advisory lock `TakeLifecycleScopeLock` and `withLifecycleScopeTx` stay: the drain still serialises per-stream delivery across reconciler instances. With no ledger to read, the lock's critical section is the RPC and the row delete alone.

### Retry gains a due time

The outbox row gains `attempt_count`, `next_attempt_at`, and `last_error`, and the drain blocks a stream whose head is not yet due, as the producer-verb dispatcher does (`lib/runtime/producer_verb_outbox.go`). Backoff is exponential from the reconciler interval to a cap of one minute. This is the one place the sketch borrows from the queue consolidation question below; it is a column and a comparison, and it stands on its own if the consolidation never happens.

### The proxy

`cmd/rimsky-host-agent-proxy/lifecycle_handler.go` is unchanged. `OnRunScopeTerminal` now arrives through the outbox: on the kick, one channel hop after the direct call would have fired, and retried on the interval if the proxy was unreachable when the scope closed. The reap is idempotent already, since `dropSpawnsForRunScope` returns nothing on a second call.

```
frame settles ──tx──▶ close scope tree ──▶ stage (peer, scope, run_scope_terminal) per peer ──commit──▶ Kick()
terminate route ──tx──▶ close scopes ──▶ stage run_scope_terminal rows ──▶ stage instance_terminated ──commit──▶ deliver inline

reconciler, on kick or tick ──▶ oldest pending per stream, due ──▶ RPC ──ack──▶ delete row
                                                      └──err──▶ attempt+1, next_attempt_at, block stream
```

### What goes, in full

- `lib/runtime/lifecycle_fanout.go`; the `scopeFanout` parameter through `frame.RunTick`, `runFrameEndDetection`, `transitionFrameEnd`; `SchedulerConfig.LifecycleSubs` and `LifecyclePeersForSpec`.
- `lib/control/controlapi/lifecycle.go`: `fanOutRunScopePeer`, `CloseAndFanOutRunScopesForInstance`, `fanOutInstanceTerminatedFromLifecycleRows`, and the direct template and instance fan-out functions the staged path superseded, if any caller remains.
- `lib/control/controlapi/lifecycle_reconciler.go::drainTerminatedInstances`; `InstanceTable.ListTerminatedWithLifecycleRows` in both backends.
- `lib/foundation/persistence/lifecycle_idempotency.go` and both backends; `rimsky_lifecycle_idempotencies` by migration; `collectRunScopeIDsForInstance` and the ledger purge in `handleDeleteInstance`; `TestDeleteInstance_PurgesRunScopeLifecycleIdempotencyRows`.
- The `lifecycle_reconciler.*` event kinds the terminated-instance pass emits; `/events` names the rest.

### Corpus

- `concept:lifecycle-subscriber`: every event is staged in its transition's transaction and drained by the reconciler; the idempotency ledger sentence goes; the "delivered by a periodic poll loop that scans for terminated instances" sentence goes. Boundaries: the settlement-time run-scope terminal still belongs to the scheduler, now as a staged row.
- `decision:lifecycle-subscriber-at-least-once-delivery`: the ledger is no longer the absorber; the outbox row deleted on ack is, and a duplicate after a crash between ack and delete is the accepted residue. Subscriber handlers stay idempotent.
- `decision:lifecycle-fanout-after-commit`: unchanged in its choice; the rationale gains that the staged row is what makes "after commit" durable.
- `concept:host-agent-proxy`: the cache fills from lifecycle notifications as before; the spawn reap arrives through the outbox and may lag the scope close by the reconciler interval.
- Issues: `issue:instance-delete-drops-undelivered-lifecycle-events` is settled by staging at the transition and is retired. `issue:lifecycle-outbox-retention-narrows-at-least-once` stays open; the trailing-retention sweep is untouched. `issue:event-log-domain-for-peer-delivery-health` stays open and gains the outbox's pending count as the candidate signal.

## Open questions

- **Where the staging package lands.** `lib/foundation/lifecycle` exists and holds the subscriber registry; the staging functions could join it, or sit in `lib/foundation/persistence` beside the table. The sketch assumes `lib/foundation/lifecycle`, since the registry and the peer-list resolver are already there.
- **How the frame engine learns the peer list.** `LifecyclePeersForSpec` takes a template spec; the frame-end path holds the instance id. The sketch assumes the staging wrapper resolves instance to template to peers in the same transaction, one read per frame end.
- **Cross-stream ordering at termination.** A peer may receive `instance_terminated` before the last `run_scope_terminal` of that instance. The proxy tolerates it. A future subscriber that provisions per-instance substrate might not; the sketch assumes the corpus states the non-guarantee rather than serialising the streams.
- **Observability shape.** The peers route reports pending outbox rows instead of ledger rows. The sketch assumes the response shape changes and the same sweep updates the route's consumers in `cmd/rimsky/cli`.
- **Kick coalescing.** A frame that closes many scopes sends one kick after its commit, not one per row; the sketch assumes the kick channel has capacity one and a kick that finds it full is dropped, as a drain is already pending.
- **Backoff cap.** One minute is a guess. The proxy's reap has a 30-second SIGTERM grace, and a spawn that outlives its scope by a minute costs a process, not correctness.

## Risks / unknowns

- The frame-end transaction grows by one read (the template spec) and N writes (one row per peer per closed scope). A fan-out frame closing many partition scopes against several peers stages many rows in one transaction. The outbox's per-stream drain handles the volume; the transaction size is the risk.
- Scenario tests under `test/scenarios/` that wait on the direct run-scope call (`instance_lifecycle_fullstack_test.go`, `parked_lifecycle_test.go`, the host-agent isolation tests) now wait on the reconciler's delivery. The kick makes the delivery follow the frame end without a tick, so a test that drives the frame to settle and waits on the peer's own event still passes; a test of the retry path fires the reconciler tick itself, per the testing standard.
- `lib/foundation/persistence/conformance/lifecycle_scope_lock.go` and `observability.go` exercise the ledger table; the conformance suite shrinks.
- The proxy's `OnInstanceTerminated` cache drop and the new `instance_terminated` staging interact with the proxy's cache-miss refill: a drop that lands before a late dispatch refills from the control API, which returns the terminated instance. The existing miss path already handles this; it is listed because the timing changes.

## What this is not

- Not the shared due-time queue. The lifecycle outbox, the producer-verb outbox, blob orphans, staging reservations, and the two sensor schedules share one shape — a per-stream FIFO whose head is eligible when due — and could share one table triple and one dispatcher. The measured saving is about 300 of 2,100 production lines in core and one contract suite per backend in place of three table suites; the gain is one backoff, one kick, one clock. Whether that is worth a sweep is unsettled and is not part of this sketch. This sketch borrows only the due-time columns.
- Not a change to the protocol. The seven RPCs, the empty ack, and the no-veto rule stand.
- Not a change to what the proxy does on each event.
- Not a change to the outbox's trailing-retention sweep or the issue it carries.
