---
concept: terminal-resolution
aliases:
  - executor-terminal-spine
---

# Terminal resolution

## What it is

Terminal resolution is the end-to-end spine that takes one executor outcome off the wire and converges it onto exactly four decisions: which canonical signal type-path to emit, and with it the verdict's tag set as the discriminator subscribers filter against; whether the node-run row settles — state-transitioned, its dispatch entry removed from the queue — or the dispatch retries in place with the row and its queue entry both preserved (see `decision:in-place-retry`); which producer verb, if any, to fire on every acquired claim; and when to delete the persisted claim-handle rows under the claimant guard. Four stages carry those decisions across the runtime. The same four stages handle an executor error outcome and a runtime acquisition failure alike: an acquisition failure routes through the operator's per-class error-policy chain by the producer-declared class, or by the handler's synthetic class where the producer declared none (see `concept:error-policy`).

"Terminal" is not a wire term. The wire layer carries a single outcome, and the unary call returns it directly. The word is reserved for two narrower senses: the state-machine sense — the terminal states of `concept:node-run` and the entry point of the unified claim-handle resolution — and this concept's name for the spine as a whole. The internal terminal-kind classification is a supervisor-internal categorization, not a wire shape.

The stages run in order:

1. **Wire to internal terminal kind.** The outcome reader maps each wire outcome variant to its own internal terminal kind: completion, error, park, or async-accepted. An absent or unrecognized outcome synthesizes a fifth kind, infra, so every dispatch path reaches a terminal kind even when a peer violates the wire contract. The settling verdict's tag set rides into the cascade walk's predicate evaluation (see `concept:terminal-tag`).
2. **Dispatch on terminal kind.** The terminal-application step routes completion, error, infra, and park to their per-kind handlers and increments a per-class verdict counter. An acquisition failure, which happens before dispatch, routes through its acquire-phase handler into the same third stage.
3. **Resolution.** This stage produces the canonical resolution tuple of signal, dispatch disposition, and color (see `concept:error-policy`). It runs the operator's per-class error-policy chain when the terminal kind is an error, or when an acquisition-failure class is in play. For completion, park, async-accepted, and infra the terminal kind fixes the resolution and no operator-configurable chain runs. The retry cap binds at a different point on each path: on the error and acquisition path the chain is consulted first and the cap applies only when the chain resolves to a retry, which is why a per-class pass never reaches the cap; on the infrastructure path no chain runs and the attempt count meets the infra retry cap first.
4. **Claim-handle resolution.** The lock-release step walks the dispatch's acquired locks. A named-lock acquisition takes a claimant-guarded handle delete and nothing else. A non-held claim goes straight to the unified claim-handle resolution under an active-terminal source. A held claim marks the persisted claim-holder row and checks the holding subgraph; once that subgraph is complete, the engine computes the aggregate outcome — a failure anywhere makes it an abandon, otherwise a commit — and calls the unified claim-handle resolution under a held-terminal source. The verify-before-run bail, where a supervisor discovers after commit that another supervisor took the dispatch and unwinds the acquisition it just opened, calls the same resolution under an ownership-bail source; that source deletes the handle row under the claimant guard and emits no signal, because the acquisition is unwound rather than resolved. The unified claim-handle resolution records the disposition, enqueues the producer verb, and resolves the persisted claim-handle row under the claimant guard, all inside the settlement transaction — one audited site for all three sources. The verb itself notifies the producer of a decision already made; it is never a decision point.

An async-accepted outcome leaves the spine and re-enters it through the callback path, and the terminal the callback eventually produces feeds back into the terminal-application step. A retry disposition loops in place on the locks the dispatch already holds, without releasing them, while a give-up, a pass, and a release-and-requeue all end at the lock-release step. Park is the exception on both counts: its resolution is fixed rather than policy-driven, and its claims are retained rather than routed to lock release.

Five acquire-phase handlers sit outside the unified engine and share one downstream path: unavailable, producer error, missing frame identity, fan-out substitution failure, and lock-spec substitution failure (see `decision:acquire-unavailable-carveout`). Each runs before dispatch, when the acquisition attempt fails. Each enqueues an abandon for whatever the producer had already opened and routes through the error path for the state-machine and queue mutation. The first two key on the producer-declared class where the producer supplied one and fall back to their own synthetic class; the other three carry no producer-declared class and key on their synthetic class alone.

## Purpose

Each of the four constituent concepts describes one stage, and none of them on its own shows how an error from an executor ends up abandoning a claim several steps later. Terminal resolution threads the spine so a reader can follow one terminal event from the wire through to the producer verb and the deletion of the claim-handle row, and so an engineer changing one stage can see what the next stage expects.

## Boundaries

Terminal resolution owns the four-stage flow as one narrative, the mapping from terminal kind to emitted signal to producer verb, and the two convergence points: the per-acquired-lock fan-out at lock release, and the per-claim-handle producer-verb site at the unified claim-handle resolution. It also owns the terminal-verb outbox, the durable, ordered, per-producer queue that carries the producer verb. Settlement enqueues the verb there instead of dialing the producer inside the settlement transaction, so the claim's disposition is decided and durably recorded before any producer is dialed, and the cascade never waits on a producer's acknowledgement. Terminal resolution owns no stage's internals; those belong to the constituent concepts.

See also `concept:executor`, `concept:signal`, `concept:terminal-tag`, `concept:error-policy`, `concept:auto-terminal`, `concept:claim-handle`, `concept:parked-state`, `concept:node-run`.

## Aliases

`executor-terminal-spine`.
