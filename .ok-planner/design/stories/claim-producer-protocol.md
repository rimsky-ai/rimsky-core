---
story: claim-producer-protocol
status: as-is
---

# Service author writes custom claim-producer

## Role

As a service author writing a custom claim-producer, I can implement the `concept:claim-producer` protocol — a capabilities advertisement plus the open / commit / abandon / release verbs — with my chosen write-semantics (sync, staged-async, blocking-async, or read-only), advertise my capabilities at startup, accept open requests with resolved scope data, return claim handles that drive the executor dispatch, and accept terminal verbs that close the claim lifecycle correctly, so that my producer plugs into a rimsky stack and rimsky orchestrates claims against it.

## Capability

Public claim-producer protocol surface — a capabilities advertisement plus the open / commit / abandon / release verbs (see `concept:claim-producer`); rimsky drives discovery, schema validation, and terminal-verb orchestration; producers advertise their write-semantics and rimsky enforces it at registration.

## Business value

A custom producer plugs into a rimsky stack and rimsky orchestrates the claim lifecycle against it — a producer the rimsky stack does not know about can ship and integrate.

## Acceptance

A custom claim-producer implementing the public protocol, registered with rimsky's catalog, is referenced from a template; on instance dispatch, the producer receives a real open call with resolved scope bytes, returns acquired or unavailable per its policy; on success, rimsky drives commit at auto-terminal; on failure, abandon; on lifecycle close, release. The producer's capabilities are honored — a template referencing a write-semantics the producer doesn't advertise is refused at registration.

## Falsifier

A registered producer's open call is bypassed, OR commit / abandon / release are called but the producer's effect is canned, OR a write-semantics the producer didn't advertise is silently accepted at registration.

## Proof

Example.
