---
concept: lifecycle-subscriber
---

# Lifecycle subscriber

## What it is

A lifecycle subscriber is a service that implements the lifecycle-subscriber protocol, through which rimsky delivers control-plane and instance-lifecycle transitions to the services that opt in. A service opts in by declaring the lifecycle-subscriber protocol alongside its primary protocol — `concept:claim-producer`, `concept:executor`, or `concept:publisher`. Any service kind may subscribe, and the slot a service fills in a template is independent of whether it subscribes. Non-subscription is the default. Rimsky stages every delivery as a row in a persisted outbox inside the transaction that performs the transition, drains the outbox in order per service and per object from every runtime role (see `decision:lifecycle-drain-per-role`), and deletes a row when the service acknowledges it, so each event is delivered at least once (see `decision:lifecycle-subscriber-at-least-once-delivery`).

## Purpose

A service that must react to a control-plane transition learns of it here. The lifecycle-subscriber protocol is the one service protocol rimsky notifies at instance creation. A subscriber reacts to a transition it observes; rimsky orders no work against that reaction, and a subscriber's error neither refuses nor rolls back the transition (see `decision:lifecycle-fanout-after-commit`). Keeping the protocol optional and separate from a service's primary protocol lets a service that implements only its primary protocol stay simple.

## Boundaries

The protocol relays the control-plane and instance lifecycle: template lifecycle transitions, instance creation, instance termination, and run-scope terminals. It deliberately carries no node-cascade event, such as an individual node run parking. Those live in `concept:signal` and `concept:event-log`, and a subscriber that must watch node-level state consumes those concepts instead.

A lifecycle subscriber owns the lifecycle event taxonomy, the opt-in mechanism, the outbox, and the drain that delivers from it. It does not own the state transitions themselves: template and instance transitions, and the run-scope terminals of an administrative termination, belong to `concept:control-api`; the run-scope terminals of a settling frame belong to the scheduler, and those of a child scope closed at rendezvous belong to `concept:supervisor`. It does not own the subscriber-side reaction either, which belongs to the subscribing service. Delivery orders events per service within one object — a template, an instance, or a run scope — and promises no order across objects: a service may receive an instance's termination before the last run-scope terminal of that instance.

Instance-keyed and run-scope-keyed events also reach the late-bound service proxies of the template (see `concept:host-daemon-proxy`).

see also: `concept:claim-producer`, `concept:executor`, `concept:publisher`, `concept:sensor`, `concept:template`, `concept:instance`, `concept:control-api`, `concept:supervisor`, `concept:host-daemon-proxy`, `concept:signal`, `concept:event-log`.
