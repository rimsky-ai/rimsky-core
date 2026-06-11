---
story: claim-producer-protocol
status: as-is
---

# Service author writes custom claim-producer

## Role

As a service author writing a custom claim-producer, I can implement the gRPC `ClaimProducer` server (`Capabilities`, `Open`, `Commit`, `Abandon`, `Release`) with my chosen write-semantics (sync / staged_async / blocking_async / read_only), advertise my capabilities at startup, accept `Open` requests with resolved scope data, return claim handles that drive the executor dispatch, and accept terminal verbs (Commit / Abandon / Release) that close the claim lifecycle correctly, so that my producer plugs into a rimsky stack and rimsky orchestrates claims against it.

## Capability

Public claim-producer protocol surface (`Capabilities`, `Open`, `Commit`, `Abandon`, `Release`); rimsky drives discovery, schema validation, terminal-verb orchestration; producers advertise their write-semantics and rimsky enforces it at registration.

## Business value

A custom producer plugs into a rimsky stack and rimsky orchestrates the claim lifecycle against it — a producer the rimsky stack does not know about can ship and integrate.

## Acceptance

A custom claim-producer implementing the public protocol, registered with rimsky's catalog, is referenced from a template; on instance dispatch, the producer receives a real `Open` with resolved scope bytes, returns Acquired or Unavailable per its policy; on success, rimsky drives Commit at auto-terminal; on failure, Abandon; on lifecycle close, Release. The producer's capabilities are honored — a template referencing a write-semantics the producer doesn't advertise is refused at registration.

## Falsifier

A registered producer's `Open` is bypassed, OR Commit/Abandon/Release are called but the producer's effect is canned, OR a write-semantics the producer didn't advertise is silently accepted at registration.

## Proof

Example.
