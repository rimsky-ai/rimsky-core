---
story: claim-producer-protocol
status: as-is
---

# Service author writes custom claim-producer

## Story

As a service author writing a custom claim-producer, I can implement the `concept:claim-producer` protocol — a capabilities advertisement plus the open / commit / abandon / release verbs — with my chosen write-semantics (sync, staged-async, blocking-async, or read-only), advertise my capabilities at startup, accept open requests with resolved scope data, return claim handles that drive the executor dispatch, and accept terminal verbs that close the claim lifecycle correctly, so that my producer plugs into a rimsky stack and rimsky orchestrates claims against it.

Public claim-producer protocol surface — a capabilities advertisement plus the open / commit / abandon / release verbs (see `concept:claim-producer`); rimsky drives discovery, schema validation, and terminal-verb orchestration; producers advertise their write-semantics and rimsky enforces it at registration.

A custom producer plugs into a rimsky stack and rimsky orchestrates the claim lifecycle against it — a producer the rimsky stack does not know about can ship and integrate.
