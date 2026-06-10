---
concept: inertness
status: as-is
aliases:
  - inert bytes
---

# Inertness (cross-cutting discipline)

## What it is

A uniform discipline applied across two overlapping lists.

**Carrier streams the discipline governs** (seven): claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, attribute values, named-event payloads, message payloads. Plus executor error payloads. Each stream is "inert" in rimsky — rimsky neither inspects nor interprets the bytes beyond a narrowly defined set of read sites.

**Read-site sub-disciplines** distinguish how strict the rule is per stream:

- **Byte-opaque inertness** — rimsky never traverses the bytes at all. Applies to: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content. Rimsky reads them only at substitution-leaf extraction or for transport into the executor's wire (per `@blessed-invariant 20` and `21`).
- **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) and for the precisely-enumerated sanctioned read sites below, but does NOT inspect values to make routing or validation decisions outside those sites. Applies to: attribute values, named-event payloads, message payloads, executor error payloads. Rimsky reads them only at the sanctioned read sites; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, pattern-matches, attaches to traces, or includes them in error messages. The "pattern-matches" prohibition still binds for the three streams without a matcher-style sanctioned site (named-event payloads, message payloads, executor error payloads); attribute values gained a sanctioned matcher read site via the shared matcher evaluator described below.

## Purpose

Rimsky is a project-agnostic substrate. Logging, normalizing, or otherwise inspecting carrier bytes would couple rimsky to the carrier's semantics. The discipline keeps rimsky narrow: the bytes go in one side and come out the other unchanged, except at the precisely-named substitution leaf and transport boundary.

## Boundaries

Owns: the cross-cutting "don't inspect" rule, the enumerated sanctioned read sites, the per-stream invariant annotations, and the two-sub-discipline taxonomy. Does NOT own: any one of the streams individually (each has its own concept and schema home). Adjacent: `concept:claim`, `concept:claim-scope`, `concept:blob-backend`, `concept:named-event`, `concept:attribute` (substitution is the sanctioned exception).

## Invariants

Three `@blessed-invariant`s codify the discipline:

- **§20** — claim payload, address, and claim scope are byte-opaque inert (carried on the claim-result value type).
- **§21** — blob content (carried by the blob-backend interface) and (by extension) named-event payloads + executor error payloads are structurally inert.
- **§24** — message payloads are inert. Read only at the substitution leaf (resolving the trigger message) and at the persistence-layer fetch that surfaces a single message row. The message delivery path touches envelope routing fields (kind, sender, sender-kind, target, frame id, delivered-at) but never the payload.

Sanctioned read sites (each carries the inertness annotation in code):

- **Substitution-leaf path walk** — traverses every inert stream at substitution time (claim payload, attribute values, named-event payloads, message payloads) to extract the leaf value named by a substitution path.
- **Top-level directive stringify** — renders top-level address / claim-scope directives during substitution.
- **Claim-handle wire encoding** — encodes the claim handle into the executor's wire structure at dispatch.
- **Message persistence fetch** — surfaces a single message row verbatim to the operator.
- **Attribute matcher evaluation** — applies to attribute values only. Reads the resolved post-L4 attribute bag to evaluate `attrs.<path>` equality predicates from `by_match` attribute-override matchers and from `concept:breakpoint` matchers. The read is primitive-equality only; no traversal beyond the named path; values not logged, not formatted, not included in error messages. Sanctioned by `concept:attribute`'s L5 matcher-overlay invariant.

## Auth audit log: verbatim request_params

The `auth.access_attempted` and `auth.access_denied` event rows store the request body verbatim as `request_params` (see `concept:event-log`). Verbatim storage is sanctioned by inertness: rimsky's structural-inertness discipline guarantees no sensitive data flows in request bodies (the only sensitive value in an auth-relevant exchange is the API key itself, which is in the `Authorization` header — never stored). Verbatim params make the audit log materially more useful for forensic queries ("show me everything `agent:supervisor:prod` did with template_hash X") without violating inertness.
