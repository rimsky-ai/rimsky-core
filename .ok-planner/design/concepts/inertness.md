---
concept: inertness
status: as-is
aliases:
  - inert bytes
---

# Inertness (cross-cutting discipline)

## What it is

A uniform discipline applied across two overlapping lists.

**Carrier streams the discipline governs:** claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, attribute values, message payloads, scratch (per `concept:executor`), executor error payloads. Each stream is "inert" in rimsky — rimsky neither inspects nor interprets the bytes beyond a narrowly defined set of read sites.

**Read-site sub-disciplines** distinguish how strict the rule is per stream:

- **Byte-opaque inertness** — rimsky never traverses the bytes at all. Applies to: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, scratch. Rimsky reads them only at substitution-leaf extraction or for transport into the executor's wire (per invariants 20 and 21).
- **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) and for the precisely-enumerated sanctioned read sites below, but does NOT inspect values to make routing or validation decisions outside those sites. Applies to: attribute values, message payloads, executor error payloads. Rimsky reads them only at the sanctioned read sites; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, pattern-matches, attaches to traces, or includes them in error messages. The "pattern-matches" prohibition still binds for the two streams without a matcher-style sanctioned site (message payloads, executor error payloads); attribute values gained a sanctioned matcher read site via the shared matcher evaluator described below.

## Purpose

Rimsky is a project-agnostic substrate. Logging, normalizing, or otherwise inspecting carrier bytes would couple rimsky to the carrier's semantics. The discipline keeps rimsky narrow: the bytes go in one side and come out the other unchanged, except at the precisely-named substitution leaf and transport boundary.

## Boundaries

Owns: the cross-cutting "don't inspect" rule, the enumerated sanctioned read sites, the per-stream invariant annotations, and the two-sub-discipline taxonomy. Does NOT own: any one of the streams individually (each has its own concept and schema home). Adjacent: `concept:claim`, `concept:claim-scope`, `concept:blob-backend`, `concept:attribute` (substitution is the sanctioned exception).

## Invariants

Three invariants codify the discipline:

- **§20** — claim payload, address, and claim scope are byte-opaque inert (carried on the claim-result value type).
- **§21** — blob content (carried by the blob-backend interface) and executor error payloads are structurally inert.
- **§24** — message payloads are inert. Read only at the substitution leaf (resolving the trigger message) and at the persistence-layer fetch that surfaces a single message row. The message delivery path touches envelope routing fields (type, sender, sender_kind, frame_id, delivered_at, received_at) but never the payload.

Sanctioned read sites are precisely enumerated by the per-stream owning concepts: each owner concept names the sites where the discipline permits a read, and each read site evaluates equality predicates on attribute paths only — no traversal beyond the named path, values not logged, not formatted, not included in error messages. The scratch carrier permits persistence and copy on mid-dispatch posting and on subsequent re-dispatch; bytes remain opaque throughout.

## Auth audit log: verbatim request bodies

Auth audit-log rows store the request body verbatim, sanctioned by inertness (see `concept:event-log`). Rimsky's structural-inertness discipline guarantees no sensitive data flows in request bodies (the only sensitive value in an auth-relevant exchange is the API key itself, carried in the auth header per `concept:control-api` / `concept:api-key` — never stored). Verbatim params make the audit log materially more useful for forensic queries without violating inertness.
