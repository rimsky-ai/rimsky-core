# Subscriber readiness gating — block without veto

## Status

- Design notes, 2026-08-08.
- Companion to `issue:lifecycle-subscribers-can-block-without-authorization`, which stays open in the intake for the sprint that takes this on.
- **Not a feature expansion.** rimsky already promises that lifecycle subscribers provision things — per-template substrate at deploy, per-instance setup at creation. This makes that promise work. What exists today is a delivery mechanism with no relationship to the work that depends on it.
- Captures a distinction worked out in conversation: a subscriber must be able to *block* dependent execution without being able to *veto* an operation.

## The situation

A lifecycle subscriber is an external service rimsky notifies as templates and instances change state. The corpus describes it one-directionally — the protocol delivers transitions so peers can react — and names three archetypes: a claim producer applying per-template substrate setup at deploy, an executor warming caches at instance creation, a publisher provisioning substrate at deploy and tearing it down at undeploy.

Two of those three are provisioning. They make an instance work. Something downstream depends on them having succeeded.

Nothing connects the two. The fan-out delivers, records the delivery in a ledger, and that is the end of the relationship. No dependent work asks whether the provisioning landed.

Six of the seven callbacks do have an effect on rimsky, but by accident rather than design: their fan-out runs inside the transaction performing the transition, and the call site propagates the subscriber's error, so an error rolls the transition back. A notification became a veto through placement. The seventh — run-scope terminal — fires after its scope closed, so it logs and proceeds.

The accident is the wrong shape in both directions. It gives a subscriber authority it should not have (deciding whether an operation is *allowed*) and withholds the guarantee it needs (that dependent work waits until provisioning is *done*).

## The distinction

**Veto** — the subscriber's error refuses the operation. The transition does not happen; the caller is told it failed. The subscriber is deciding permission.

**Block** — the operation succeeds. Work that depends on what the subscriber provisions does not start until the subscriber has acknowledged. The subscriber decides nothing; it participates in ordering.

These compose: transitions never fail because a peer was down, and nothing runs against substrate that is not there yet.

Refusing an operation already has a home. `concept:validation` exists for it, and its story promises findings surfaced as blocking errors or informational warnings at registration time. A peer that needs to refuse a template implements validation. A peer that needs to prepare for one subscribes.

## What exists, and what is missing

Two of the three pieces are already built.

Delivery is at-least-once. `concept:lifecycle-subscriber` commits to it: a delivery attempt is retried until the ledger records success, and every delivery site guards its per-peer check-deliver-mark section with an advisory lock inside one transaction, so racing fan-outs converge to a single delivery per peer.

The ledger exists — keyed by service, event type, and object.

The missing piece is that **nothing reads the ledger to decide whether work may proceed**. It answers one question today: have I already delivered this, so a replay is a no-op. It has never been asked: is this peer ready. Turning it into a readiness gate — or giving it a sibling that serves that role — is the whole of the new mechanism.

## What an unacknowledged delivery should block

Not uniform across the seven callbacks, because the archetypes differ.

| Event | What a subscriber provisions | What should wait |
|---|---|---|
| template deployed | per-template substrate | creating instances of that template |
| instance created | per-instance setup, warm caches | that instance's first node dispatch |
| run-scope terminal | nothing — it reports a closure | nothing |
| template undeployed / deregistered | teardown | nothing; the dependent thing is being removed |

Teardown is the clarifying row. Blocking on it buys nothing, because whatever would wait is going away. So the gate is not a property of the protocol — it is a property of each event's relationship to downstream work, and it has to be decided per event rather than switched on wholesale.

## Open questions

- **Where does blocked work wait?** rimsky already has `concept:parked-state` for a run that cannot proceed yet. Whether a gate reuses parking, or holds at a different boundary (instance creation is not a node run and cannot park), needs settling.
- **What does a permanently failing subscriber do to a deployment?** At-least-once retries forever. A peer that never succeeds means instances that never start — correct, but it must be observable as that, not as an unexplained stall. This is the failure mode most likely to be experienced as a bug.
- **Does an operator need an override?** A stuck provisioning gate with no way past it is a deployment that cannot make progress. Whether the answer is a forced-ready action, a timeout into a visible degraded state, or nothing at all is a real decision.
- **Do the two claim producers that subscribe today want any of this?** Both the bundled Postgres and filesystem producers register the shared implementation whose seven callbacks are bare no-ops. They subscribe so the protocol is exercised. If the archetypes the concept advertises are not implemented anywhere in the tree, the gating design has no in-repo consumer to validate against, and the first real one will shape it.
- **What happens to the archetypes if gating is not built?** Then the concept doc advertises provisioning use cases the platform cannot support safely, and the honest move is to narrow what it claims rather than leave the gap.

## Not in scope here

Node-cascade events. The concept draws that boundary deliberately: individual node-run transitions live in `concept:signal` and `concept:event-log`, and a subscriber needing them consumes those instead. Nothing in this sketch changes that.
