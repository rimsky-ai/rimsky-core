---
concept: inertness
---

# Inertness (cross-cutting discipline)

## What it is

Inertness is one discipline rimsky applies to every carrier stream that passes through it: claim scope, claim address, claim payload, blob content, attribute values, message payloads, scratch, and executor error payloads. A stream is inert when rimsky neither inspects nor interprets its bytes outside a narrow set of sanctioned read sites. The discipline holds at two strengths. **Byte-opaque inertness** treats the bytes as meaningless outside the sanctioned sites: rimsky does not log them, format them, validate them beyond a schema gate, or otherwise interpret them. It governs claim scope, claim address, claim payload, blob content, and scratch, which rimsky reads only to extract a substitution leaf at a named path, to compare one claim scope with another for conflict, to move blob content between inline storage and its backend, and to carry a claim payload or address onto the executor's wire. **Structural inertness** lets rimsky traverse the bytes for transport mechanics and for content matching at sanctioned sites, and forbids inspecting a value to reach a routing or validation decision anywhere else. It governs attribute values, message payloads, and executor error payloads. Two sanctioned sites traverse for matching rather than transport: the shared matcher compares one named attribute path for primitive equality and walks no further, and a node-subscription payload predicate evaluates an expression over the emitted signal payload, spanning all three structurally inert streams.

## Purpose

Inertness keeps rimsky project-agnostic. Logging, normalizing, or inspecting a carrier's bytes would couple rimsky to the semantics of whoever authored them. Under the discipline the bytes enter one side and leave the other unchanged, except at the named substitution leaf and the transport boundary. The same rule extends from bytes to vocabulary. Executor-specific material stays behind the executor protocol. Rimsky's core, scheduler, and persistence surfaces carry no executor-specific field and no executor-specific term, and executor-private state rides the generic opaque carriers, chiefly scratch (see `concept:executor`).

## Boundaries

Inertness owns the cross-cutting rule that rimsky does not inspect carrier bytes, the two-strength taxonomy, and the requirement that each stream's owning concept enumerate that stream's sanctioned read sites. It owns no single stream; each stream has its own concept and its own declared shape.

No read site reads a value beyond its own contract. A concept that derives a record from a stream may hash an inert value strictly to keep the bytes out of that record, as `concept:lineage-record` does for a claim scope. That sanction covers the derived record, not hashing at large. Storing a control-plane request body verbatim in the auth audit log is a sanctioned use of the discipline (see `decision:auth-audit-log-verbatim-bodies`).

see also: `concept:claim`, `concept:claim-scope`, `concept:blob-backend`, `concept:attribute`, `concept:message`, `concept:executor`, `concept:event-log`, `concept:signal`, `concept:node-subscription`.
