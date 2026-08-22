---
concept: lifecycle-subscriber
---

# Lifecycle subscriber

## What it is

A lifecycle subscriber is a peer service that implements the lifecycle-subscriber protocol, through which rimsky delivers control-plane and instance-lifecycle transitions to the peers that opt in. A service opts in by declaring the lifecycle-subscriber protocol alongside its primary protocol — `concept:claim-producer`, `concept:executor`, or `concept:publisher`. Any peer-service kind may subscribe, and the slot a service fills in a template is independent of whether it subscribes. Non-subscription is the default. Rimsky tracks each delivery in a persisted idempotency ledger and delivers at least once (see `decision:lifecycle-subscriber-at-least-once-delivery`).

## Purpose

A peer that must react to a control-plane transition learns of it here. The lifecycle-subscriber protocol is the one peer protocol rimsky notifies at instance creation. A subscriber reacts to a transition it observes; rimsky orders no work against that reaction, and a subscriber's error neither refuses nor rolls back the transition (see `decision:lifecycle-fanout-after-commit`). Keeping the protocol optional and separate from a service's primary protocol lets a service that implements only its primary protocol stay simple.

## Boundaries

The protocol relays the control-plane and instance lifecycle: template lifecycle transitions, instance creation, instance termination, and run-scope terminals. It deliberately carries no node-cascade event, such as an individual node run parking. Those live in `concept:signal` and `concept:event-log`, and a subscriber that must watch node-level state consumes those concepts instead.

A lifecycle subscriber owns the lifecycle event taxonomy, the opt-in mechanism, the fan-out timing, and the idempotency ledger. It does not own the state transitions themselves: template and instance transitions and the administrative-termination run-scope terminal belong to `concept:control-api`, the settlement-time root run-scope terminal to the scheduler, and sub-graph and fan-out-partition run-scope terminals to `concept:supervisor`. It does not own the subscriber-side reaction either, which belongs to the subscribing service. Every fan-out runs after its transition commits. Instance termination is delivered by a periodic poll loop that scans for terminated instances, not by the request that terminated the instance, so the poll interval bounds that event's delivery latency.

Instance-keyed and run-scope-keyed events also reach the late-bound service proxies of the template (see `concept:host-agent-proxy`).

see also: `concept:claim-producer`, `concept:executor`, `concept:publisher`, `concept:sensor`, `concept:template`, `concept:instance`, `concept:control-api`, `concept:supervisor`, `concept:host-agent-proxy`, `concept:signal`, `concept:event-log`.
