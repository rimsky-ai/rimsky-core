---
issue: stub-mode-signature-no-proto-surface
kind: human
category: unspecified
artifacts:
  - concept:conformance
  - concept:executor
status: verified
opened: 2026-07-22T02:37:13Z
---

# A safety-critical handshake exists only as string literals retyped in seven places

Rimsky's conformance suite avoids firing real requests at a production executor (a service that does the actual work behind a graph node — an LLM call, an HTTP call) by first sending a probe: a request carrying the flag `stub_probe`, which a well-behaved test double answers with the response shape `{stub: true}`. That little handshake is the thing standing between the test suite and real API spend — and it exists nowhere except as a pair of string literals typed out independently at five call sites in the conformance library and reimplemented from scratch in both stub-capable executors. It's not a field in the wire protocol, not documented anywhere, and not backed by a shared definition. Rename one literal in a cleanup and nothing catches it; the only symptom is an unexplained conformance failure.

One executor also carries two sibling flags (`probe_park`, `probe_cancel`) following the identical unschematized pattern — any fix here presumably covers them too. The design corpus records the hard-coded state as a fact, not a choice; no decision document has ever ruled on it.

## Options

- **A typed field on the wire protocol.** Schema-checked, visible to every language implementation, immune to a silent rename — but a breaking protocol change touching every executor, in-tree and third-party, to carry what is fundamentally a test-tooling concern.
- **Document it as an official contract**, no code change. Cheapest; turns tribal knowledge into something a third-party implementer can read — but nothing mechanically ties the prose to the literals, so code can still drift from doc.
- **Centralize the literals in one shared Go definition** that every call site imports. Drift becomes greppable and mechanically checkable without touching the protocol; a non-Go third party still relies on documentation.

These compose: a protocol field would still want docs, and centralizing could be a stepping stone to either.

The ruling decides: typed field, documented convention, shared definition, some combination, or leave it — and whether the sibling probe flags ride along.

## Ruling

> Recommended ruling (/recommend-rulings): Centralize the probe/stub
> literals (stub_probe, stub, probe_park, probe_cancel) in one shared
> Go definition every conformance and executor call site imports, and
> document the convention in concept:conformance. No wire-protocol
> field.
>
> Rationale: One point of truth makes drift greppable and mechanically
> checkable without a proto break rippling through every executor;
> stub mode is a dev/test concern that doesn't warrant a typed field
> on the production protocol. Third parties get the documented
> contract.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
