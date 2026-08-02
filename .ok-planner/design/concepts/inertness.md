---
concept: inertness
aliases:
  - inert bytes
---

# Inertness (cross-cutting discipline)

## What it is

A uniform discipline applied across two overlapping lists.

**Carrier streams the discipline governs:** claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, attribute values, message payloads, scratch (per `concept:executor`), executor error payloads. Each stream is "inert" in rimsky — rimsky neither inspects nor interprets the bytes beyond a narrowly defined set of read sites.

**Read-site sub-disciplines** distinguish how strict the rule is per stream:

- **Byte-opaque inertness** — rimsky treats the bytes as meaningless outside the enumerated sanctioned sites below; it does not log, format, validate beyond schema gates, or otherwise interpret them. Applies to: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, scratch. Rimsky reads them only at substitution-leaf extraction (which may walk to a named path within the bytes), at the byte-equality conflict comparison (claim scope), at blob-spill movement between the inline column and the backend (blob content), or for transport into the executor's wire (claim payload, address); each owning concept enumerates its own stream's exact sites.
- **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) and for the precisely-enumerated sanctioned read sites below, but does NOT inspect values to make routing or validation decisions outside those sites. Applies to: attribute values, message payloads, executor error payloads. Rimsky reads them only at the sanctioned read sites; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, indexes, attaches to traces, or includes them in error messages, except as the sanctioned sites below require. Two sanctioned sites traverse for content-based matching rather than plain transport: the shared matcher evaluator performs primitive-equality comparison against a single named attribute path, with no traversal beyond that path; node-subscription payload predicates evaluate a CEL expression over the emitted signal payload, spanning all three structurally-inert streams (attribute-delta, message-body, and error-payload fields, each validated against the receiver's schema at registration). A downstream owning concept may compute a value hash strictly to keep inert bytes out of a derived record it owns (for example, the lineage record's attribute-bag hash) — this sanctions that one derived-record use, not hashing generally.

## Purpose

Rimsky is a project-agnostic substrate. Logging, normalizing, or otherwise inspecting carrier bytes would couple rimsky to the carrier's semantics. The discipline keeps rimsky narrow: the bytes go in one side and come out the other unchanged, except at the precisely-named substitution leaf and transport boundary.

The same leveling discipline extends beyond bytes to vocabulary: anything executor-specific lives behind the executor protocol, and rimsky's core, scheduler, and persistence surfaces carry no executor-specific fields, tables, or terms — executor-private state (session material, resume context, checkpoints) rides the generic opaque carriers, chiefly scratch (see `concept:executor`).

## Boundaries

Owns: the cross-cutting "don't inspect" rule, the enumerated sanctioned read sites, the per-stream invariant annotations, and the two-sub-discipline taxonomy. Does NOT own: any one of the streams individually (each has its own concept and schema home). Adjacent: `concept:claim`, `concept:claim-scope`, `concept:blob-backend`, `concept:attribute` (substitution is the sanctioned exception), `concept:message`, `concept:executor`.

## Invariants

Three invariants codify the discipline:

- **§20** — claim payload, address, and claim scope are byte-opaque inert (carried on the claim-result value type).
- **§21** — blob content (carried by the blob-backend interface) is byte-opaque inert; executor error payloads are structurally inert.
- **§24 (message-inertness)** — message payloads are inert. Read only at the substitution leaf (resolving the trigger message), at persistence-layer fetches that surface message rows (single or list), and at delivery time, when the message-receiver-node's attribute bag is populated from the message body under the same structural-inertness discipline that governs attribute values. The message delivery path also touches envelope routing fields (type, sender, sender_kind, frame_id, instance_id, cancelled, delivered_at, received_at).

Sanctioned read sites are precisely enumerated by the per-stream owning concepts: each owner concept names the sites where the discipline permits a read. Read shapes vary by site — verbatim extraction (a substitution-leaf read returns the resolved value unchanged), equality comparison (the claim-scope conflict predicate, the shared matcher evaluator), or wire transport (executor dispatch) — but no site inspects a value for a purpose beyond its own narrow contract, and no site logs, formats, or includes the value in an error message. The scratch carrier permits persistence when scratch arrives attached to a settling outcome and copy onto the subsequent re-dispatch (there is no mid-dispatch scratch write channel, per `concept:executor`); bytes remain opaque throughout.

## Auth audit log: verbatim request bodies

Auth audit-log rows store the request body verbatim, sanctioned by inertness (see `concept:event-log`). This is a deliberate policy choice, not a consequence of the inertness discipline itself: control-plane request bodies are treated as carrying no secrets, since the one sensitive value in an auth-relevant exchange — the API key — travels in the auth header per `concept:control-api` / `concept:api-key` and is never stored. Verbatim params make the audit log materially more useful for forensic queries without violating inertness.
