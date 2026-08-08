---
issue: publisher-declared-kinds-unenforced
kind: human
category: enforcement-gap
artifacts:
  - concept:publisher
  - concept:template
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-08T05:02:01Z
---

# A template can name a publisher kind no peer implements, and registration accepts it

A rimsky template may declare publishers: external services that feed messages
into a workflow. Each declaration names a **kind** — which flavour of publisher
this is — and rimsky dials the peer that serves it.

Nothing checks that the named kind is one the peer actually offers. Template
registration validates that a publisher has a name, that names are unique, that
the kind field is non-empty, and that the message type it declares appears in the
template's message registry. It never asks the peer what kinds it supports, and
it has no way to: the validator's hook set carries lookups for executors, stores,
named locks, and claim-producer error classes, and has no publisher hook at all.

So a typo in a kind registers cleanly and fails later, at dispatch, in a
deployment rather than at the desk of the person who wrote it.

## Why it surfaced

The examples module's publisher README asserted this enforcement exists. It
doesn't. The README is being deleted along with the rest of that module, but the
claim it made is one a reasonable reader would expect to be true — every other
peer-facing declaration in a template is checked against what the peer
advertises, which is what makes this one conspicuous.

## Options

- **Add the hook and the check.** A publisher-supported-kinds lookup alongside the
  existing executor and claim-producer hooks, consulted at registration. Consistent
  with how every other peer declaration is validated; costs a capability call at
  registration and a new hook on a struct several call sites construct.
- **Leave it to dispatch.** Registration stays cheap and stays possible against a
  deployment whose publisher peers are not yet reachable. The cost is that the
  failure lands at runtime, where a workflow author cannot see it and an operator
  cannot interpret it.

The ruling decides whether a publisher kind is checked when the template is
registered or when a message needs to move.

## Ruling

> Add the hook and check the declared kind at registration, matching how
> executors and claim-producers are already validated against their peers.
>
> Rationale: the project's grain is that a template naming something a peer cannot
> provide is refused at registration — that is why the hook set exists at all, and
> a publisher is the only peer declaration outside it. The peer's half of this
> contract is already built and already enforced: a publisher advertises its kinds
> in its capabilities handshake, and the conformance suite fails a peer that serves
> a kind it did not advertise. The data is there and verified; rimsky simply never
> asks for it at registration.
>
> Nothing records a reason for the gap. No decision covers it and the publisher
> concept is silent, while the hook set demonstrably accretes one lookup at a time
> as specific needs arise. This reads as an omission, not a choice.
