---
concept: node-subscription
aliases: [subscription]
---

# Node-subscription

This concept describes the **receiver-side** template-DSL subscription declared on a node — a node's wait-set on a sibling's signal, targeting any of the signal families a subscription may match. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal.

## What it is

A node-subscription declares a target signal (a canonical signal type-path, exact or wildcarded per `concept:signal`) plus an optional payload predicate, plus a required force-upstream-refresh cascade-shape boolean. A sender-side filter selects the specific upstream node-type the subscription reacts to. Subscriptions are declared per node in the template.

A subscription's target is restricted to the settling signal kinds that fire cascade — `terminal/success`, `terminal/error/<class>`, and `attribute/<key>/changed` — per `concept:cascade`'s single firing-gate predicate. Dispatch-internal signals are not subscribable; declaring a subscription against one is rejected at registration. To express interest in a specific terminal-tag discriminator, a subscription pairs a wildcarded terminal target with a payload predicate over the signal's tags. The payload predicate carries the discriminator at the payload layer rather than at the type-path leaf.

A subscription declaration is itself a wake declaration: a matching emission from the sender always stale-marks the receiver and inserts a wait-set row gating its dispatch on the sender. There is no way to declare a subscription that gathers a sender's data into the receiver's substitution context without also waking the receiver.

The force-upstream-refresh flag governs whether the receiver's invalidation drags the sender into the same frame: when set, it invalidates the sender so it re-runs in the same frame before the receiver dispatches; when unset, it leaves the sender wherever it is.

Every substitution ref in a node's attribute schema is matched at registration against the receiver's subscription declarations; a ref with no covering entry is rejected.

## Purpose

Decouple reactive coupling from compound dependency declarations. Read access, cascade subscription, and eligibility gating become independent primitives:

- Read access lives in the substitution grammar.
- Cascade coupling lives in subscriptions.
- Eligibility gating lives in `concept:wait-set`.

Cascade flow is impactee-declared.

## Boundaries

Owns:
- The per-template inverse-edge map computed at template registration from declared subscriptions plus runtime-injected structural-root edges. The runtime-injected edges originate from an empty-typed sender, one per structural root receiver (a node with no upstream of any kind: no non-self subscribes entries, no upstream attribute substitution refs, and no message-body consumption — the latter two being sugar-form subscriptions that derive real edges), with the force-upstream-refresh flag unset and the signal pattern matching a success terminal. The augmentation is template-determinable and lives on the runtime's derived in-memory map; the canonical template hash is over the spec bytes only and is unaffected by the derived view.
- The force-upstream-refresh cascade-shape contract on every subscription entry.
- The registration-time coverage check that matches every substitution ref against the receiver's subscription declarations.
- The consumer-side mapping from signal type-path to receiver wait-set rows.

Does NOT own:
- The signal taxonomy itself or payload schemas (those live in `concept:signal`).
- The cascade walk itself (lives in `concept:cascade`).
- The wait-set ledger that drives dispatch eligibility (lives in `concept:wait-set`).
- The dispatch-eligibility predicate that selects ready node-runs (a persistence-layer query).

## Invariants

- The subscription target and payload predicate are validated at registration against the subscribable subset of the canonical taxonomy (`concept:signal`, gated by `concept:cascade`'s firing-gate predicate) and the resolved payload schema. A target outside that subset is rejected by name.
- Every substitution ref in a node's attribute schema is matched by at least one subscription entry whose sender and type would deliver the corresponding signal. Templates with uncovered refs are rejected at registration.
- Every subscription entry carries the force-upstream-refresh cascade-shape flag; entries missing it are rejected at registration.
- **Self-subscription is first-class in both same-frame and next-frame shapes** — the "drain my own queue" idiom has two equally-valid spellings. The next-frame shape opens a fresh frame for the same node-instance on every matching commit (one frame per queue item, clean frame boundaries per iteration). The same-frame shape keeps iteration inside the current frame (one long-running frame, supervisor picks up each new pending run as it lands). The cascade walker's insert-then-drain-in-same-tx pattern makes the same-frame shape safe: the new pending self-run's wait-set blocker (keyed on the just-committed run) is drained at the end of the terminal-complete handler in the same transaction, before the supervisor sees it. The cascade stale-mark does not touch the per-instance node row's state — it only inserts a new run row and re-stamps the frame reference — so the just-committed fresh state survives intact.
