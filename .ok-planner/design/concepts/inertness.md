---
concept: inertness
status: as-is
aliases:
  - opacity (legacy)
  - inert bytes
references:
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
  - _discover/2026-05-10-attribute-substitution-grammar.md
  - _discover/2026-05-10-blob-spill-pluggable-backends.md
---

# Inertness (cross-cutting discipline)

## What it is

A uniform discipline applied across two overlapping lists.

**Carrier streams the discipline governs** (seven, post-2026-05-21): claim scope, claim address, claim payload, blob content, attribute values, named-event payloads, message payloads. Plus the `Error.payload` Struct from the post-2026-05-12 proto restructure. Each stream is "inert" in rimsky — rimsky neither inspects nor interprets the bytes beyond a narrowly defined set of read sites.

**Read-site sub-disciplines** distinguish how strict the rule is per stream:

- **Byte-opaque inertness** — rimsky never traverses the bytes at all. Applies to: claim scope, claim address, claim payload, blob content. Rimsky reads them only at substitution-leaf extraction (`walkPath`) or for transport into the executor's wire (per `@blessed-invariant 20` and `21`).
- **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) but does NOT inspect values to make decisions. Applies to: attribute values, named-event payloads, message payloads, `Error.payload`. Rimsky reads them only at substitution leaves and event-ledger writes; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, pattern-matches, attaches to traces, or includes them in error messages.

## Purpose

Rimsky is a project-agnostic substrate. Logging, normalizing, or otherwise inspecting carrier bytes would couple rimsky to the carrier's semantics. The discipline keeps rimsky narrow: the bytes go in one side and come out the other unchanged, except at the precisely-named substitution leaf and transport boundary.

## Boundaries

Owns: the cross-cutting "don't inspect" rule, the enumerated sanctioned read sites, the per-stream invariant annotations, and the two-sub-discipline taxonomy. Does NOT own: any one of the streams individually (each has its own concept and schema home). Adjacent: `concept:claim`, `concept:scope`, `concept:blob-backend`, `concept:named-event`, `concept:attribute` (substitution is the sanctioned exception).

## Invariants

Three `@blessed-invariant`s codify the discipline:

- **§20** — claim payload, address, scope are byte-opaque inert (`foundation/locks/types.go::ClaimResult`).
- **§21** — blob content (`foundation/persistence/blob.go::BlobBackend`) and (by extension) named-event payloads + the `Error.payload` Struct are structurally inert.
- **§24** (post-2026-05-15) — message payloads are inert. Read only at the substitution leaf in `graph/attribute/substitution.go::resolveTrigger` (via `walkPath` against the trigger message) and at the persistence-layer fetch in `control/controlapi/messages.go::handleGetMessage`. The message delivery path (`runtime/message_delivery.go`) touches envelope routing fields (kind, sender, sender_kind, target, frame_id, delivered_at) but never `payload`.

Sanctioned read sites:

- `walkPath` (substitution leaf in `graph/attribute/substitution.go`) — applies to every inert stream traversed at substitution time (claim payload, attribute values, named-event payloads, message payloads).
- `stringifyRaw` (same file; top-level address/scope directives).
- `makeClaimHandle` (wire-encoding into the executor's `google.protobuf.Struct` at `runtime/runner_dispatch.go`).
- `handleGetMessage` (persistence-layer fetch surfacing the message row verbatim to the operator at `control/controlapi/messages.go`) — added 2026-05-15 for message payloads.

## Aliases and historical names

Renamed from `concept:opacity` per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #17). Adopts the two-sub-discipline framing (byte-opaque vs structural).

## Open within this concept

- "Single sanctioned introspection site" claim (substitution.go comment) vs three actual sites — see `tensions/substitution-introspection-site-count.md`.

## Auth audit log: verbatim request_params

The `auth.access_attempted` and `auth.access_denied` event rows store the request body verbatim as `request_params` (see `concept:event-log`). Verbatim storage is sanctioned by inertness: rimsky's structural-inertness discipline guarantees no sensitive data flows in request bodies (the only sensitive value in an auth-relevant exchange is the API key itself, which is in the `Authorization` header — never stored). Verbatim params make the audit log materially more useful for forensic queries ("show me everything `agent:supervisor:prod` did with template_hash X") without violating inertness.

## Notes

- Renamed from `concept:opacity` per `spec:2026-05-12-nomenclature-resolution` (audit cross-layer #17). Adopts two-sub-discipline framing.
- [2026-05-15] Clarifying addition: auth audit records store `request_params` verbatim (justified by structural-inertness + claim/payload-inert invariants — no secrets in any control-plane request body). Added by `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`.
- 2026-05-21 — Userdata collapse. `concept:userdata` retires; `@blessed-invariant 11` retires. Attribute-value inertness covered by the structural-inertness discipline. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.
