# Lifecycle-Subscriber Protocol Revisit — Design Sketch

**Date:** 2026-07-24
**Status:** Sketch (not a sprint; not authorization to build)

## Idea

Revisit the lifecycle-subscriber protocol as a whole, fixing two related problems in one design pass. First, the **firing-site labeling conflict**: `concept:lifecycle-subscriber` says main-scope run-scope-terminal fires "synchronously within the request that performs the transition," while the code actually fires it from the control-api's poll loop (`lib/control/controlapi/instance_terminator.go`, `InstanceTerminator.tick` → `CloseAndFanOutRunScopesForInstance`). Second, the **blocking-delivery structure**: delivery is a synchronous outbound gRPC (`OnRunScopeTerminal`, made inside `fanOutRunScopePeer` in `lib/control/controlapi/lifecycle.go`) executed inside a DB transaction under the per-scope advisory lock — network I/O inside a tx, so a slow or hung subscriber pins a transaction and a lock. The owner ruled (2026-07-24) that the status quo is by-design *for now*; this sketch is the parked future revisit, so the thinking isn't lost when the queue row is retired.

## Shape

**Part 1 — reconcile the trigger labeling.** Whichever way the delivery mechanics land, the concept doc and the code must tell the same story about *when* each event class fires. Options, cheapest first:

- Correct the concept's Boundaries/invariant wording to match the code: main-scope run-scope-terminal is poll-driven (like instance-terminated), not request-synchronous. One corpus delta, no code change.
- Or move the firing into the terminating request path so the doc's wording becomes true. More code, and it couples request latency to fan-out — probably the wrong direction given Part 2.

If Part 2 ships, the labeling question dissolves: every event class becomes "recorded synchronously, delivered asynchronously," and the concept describes that one uniform model instead of a three-site synchronous/asynchronous split.

**Part 2 — outbox delivery.** Replace deliver-inside-the-transaction with a transactional outbox:

```
state transition (tx)                    delivery loop (own goroutine/process)
┌──────────────────────────┐             ┌─────────────────────────────────┐
│ 1. apply the transition  │             │ 4. claim due outbox rows        │
│ 2. INSERT outbox row per │  ──────▶    │ 5. gRPC OnRunScopeTerminal etc. │
│    subscribed peer       │   (poll)    │ 6. mark delivered / retry with  │
│ 3. commit                │             │    backoff                      │
└──────────────────────────┘             └─────────────────────────────────┘
```

- The tx writes intent (an outbox row per peer per event) and commits immediately — no network I/O under the tx or the advisory lock. Atomicity between transition and delivery-intent is free because both are rows in the same commit.
- A delivery loop (shaped like the existing `InstanceTerminator` poll loop, or folded into it) claims rows, makes the outbound calls, and marks success. The existing idempotency ledger keyed by (service, event type, object) stays as the exactly-once-effect guard on the subscriber side; the outbox retry gives at-least-once attempts. The current per-scope advisory-lock section (`[check ledger row, deliver, mark row]`) shrinks to `[claim outbox row]` — the delivery itself needs no scope lock.
- Both firing processes need it: control-api (template events, instance-created, main-scope terminals) and the supervisor (sub-graph / fan-out-partition terminals, via its own subscriber registry). The supervisor either gains its own outbox table or writes to the shared one — shared is simpler and both already share the database.
- Ordering: per-(peer, object) delivery order preserved by claiming rows in insert order per key; cross-object ordering was never promised.

**Corpus impact.** `concept:lifecycle-subscriber`'s first invariant ("fires synchronously... a slow subscriber holds up the firing process's path") is deliberately reversed: the new invariant is that no subscriber can block a rimsky-side transaction. The at-least-once/idempotency invariant and the advisory-lock invariant get rewording; the fan-out candidate-set and template-spec-bytes invariants are untouched.

## Open questions

- Does the delivery loop live per-firing-process (control-api loop + supervisor loop) or as one shared drainer? Shared is simpler but gives the supervisor a control-api dependency for delivery it currently does itself.
- Latency budget: template-deploy reactions (claim-producer substrate setup) currently complete before the deploy request returns; with an outbox they complete a poll-interval later. Is any caller relying on deploy-returns-implies-substrate-ready? (The archetypes in the concept's Purpose suggest yes for claim-producers — may need a "deliver eagerly, fall back to outbox" hybrid or a deploy-time synchronous exception.)
- Outbox reaping: delivered rows need a retention/prune policy (the existing ledger is kept indefinitely; the outbox shouldn't be).
- Does `max_quiet_period`-style deadlining apply — i.e., is there a point where undeliverable rows go terminal with an event-log record instead of retrying forever?

## Risks / unknowns

- The synchronous-delivery semantics may be load-bearing somewhere untracked: any code path that assumes "transition committed ⇒ subscribers already reacted" breaks silently under an outbox. Needs a sweep before building.
- Two firing processes sharing one outbox table reintroduces the cross-replica coordination the advisory lock currently handles; row-claim semantics (e.g. `FOR UPDATE SKIP LOCKED` on postgres, and an equivalent for sqlite) must be designed for both backends.
- Scenario tests that today observe subscriber effects immediately after a transition will need event-based synchronization on delivery, not reordering-sensitive sleeps (per the determinism rule).

## What this is not

- Not a change to the lifecycle event taxonomy, the opt-in mechanism, or the node-cascade boundary (node events stay in `concept:signal` / `concept:event-log`).
- Not a wire-protocol change for subscribers: `OnRunScopeTerminal` and friends keep their shapes; only *when and from where* rimsky calls them changes.
- Not scheduled work. The owner's 2026-07-24 ruling stands: status quo is by-design until this revisit is taken up via `/plan-sprint`.
