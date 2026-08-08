---
issue: typed-event-oneof-is-aspirational
kind: human
category: conflicting
artifacts:
  - concept:event-log
  - decision:event-log-kind-enum
  - decision:event-log-payload-shapes
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T09:45:22Z
github: https://github.com/rimsky-ai/rimsky-core/issues/82
---

# The typed event schema describes events rimsky does not emit, and omits ones it does

rimsky records what happens during a run — a node started, a lock was acquired,
a template failed validation — into an append-only event log. Alongside the log
there is a protobuf schema declaring a typed message per event kind. That schema
is the thing external consumers read to learn what an event looks like.

Nothing constructs it. Not one line of rimsky builds the typed `Event` message;
the write and read paths carry payloads as free-form JSON end to end, and
consumers pull the payload column out of the database as JSON. The schema file
declares no service either, so there is no RPC that could ever carry a typed
`Event` off the wire. It exists solely as a description of the JSON — and as a
description, it is wrong in both directions at once.

Five declared payload messages describe events that are never emitted:
claim-acquired, claim-held, claim-resolved, attributes-committed, and
attributes-validation-failed. Their constructor functions have zero call sites
anywhere outside the file that declares them. And the inverse: seven kinds
rimsky genuinely does emit have no message at all — among them template
validation failures, both claim-resolution outcomes, breakpoint hits, and
applied debug overrides. So a consumer reading the schema prepares for events
that never come and is unprepared for the ones that do.

The reason this drifted so far is that nothing holds it in place. A typed
schema normally can't rot, because the code that constructs it stops compiling
— but here no code constructs it, so the compiler has no opinion, and the
declared messages read as live because nothing distinguishes them from the ones
with emitters.

The corpus has already noticed half of this. The decision governing payload
shapes states outright that "no production code constructs the proto `Event`
message" and that rimsky uses free-form JSON internally for both event classes.
What it commits to is "typed oneof payloads for a settled subset" — without
naming which kinds are in that subset. So it does not decide whether the five
unemitted messages should gain emitters or be retired, nor whether the seven
emitted kinds should gain messages.

Worth noting what rimsky already does correctly one layer over: the signal side
of the event vocabulary has typed Go payload structs that the emitters actually
construct, so its schema and its emissions cannot diverge without someone
editing both. The operational-event side has the same idea implemented as a
proto nobody constructs.

## Options

- **Retire the typed schema and derive the published one from the emitters,**
  following the pattern the signal side already uses. Costs a proto surface
  removal and the work of typing the emit sites; ends the drift structurally.
- **Reconcile the schema with reality** — add messages for the seven emitted
  kinds, delete or wire the five unemitted ones. Restores accuracy, but leaves a
  schema nothing constructs, so it starts drifting again the day it merges.
- **Keep it and label it aspirational** in the decision record and the schema
  itself, so readers know it describes intent rather than behavior. Cheapest, and
  the least defensible to an external consumer who wanted a contract.

The ruling decides whether rimsky's event schema is a contract it holds itself
to, or a document it retires.

## Ruling

> Generate the Go types from the events proto and construct event payloads with
> them. Stop hand-rolling payloads as loose maps — the declaration goes into the
> path that produces the data, rather than sitting beside it describing what the
> data is supposed to look like.
>
> Rationale: the nine other protos in the published package declare a service,
> so their generated types sit on both sides of every call and a drifted field
> stops compiling — on top of which the conformance suites drive real peers
> against them. Those contracts stay honest because the declaration is
> unavoidable, not because anyone watches them. The events proto is the one file
> with no service, so nothing is generated that anyone calls, and its payload
> half drifted to five phantom messages and seven missing ones. The fix is to
> give it the same property the other nine have rather than to remove it:
> emitting an event should be impossible without going through the declared
> type. Retiring it would delete a wrong document; wiring it deletes the reason
> documents go wrong.
>
> The field-level mismatches filed as
> `issue:events-proto-payloads-disagree-with-emitters` are this same question one
> level down and are resolved by the same work — once the payload is built from
> the generated type, a declared field with no writer and a written key with no
> declaration both become impossible.
