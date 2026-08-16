---
audit: claim-handoff
artifact: story:claim-handoff
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:22:00Z
---

# One claim opened once, shared across a subgraph, and resolved atomically

Supported. Driving a stack from this tree through the template surface and the
control API, an acquirer node opened a claim on a selector and two downstream
nodes co-held it; the producer's own verb log shows one Open for the whole
holding subgraph and one terminal verb, and both co-holders received the
acquirer's address byte-for-byte along with a payload field and the scope
bytes, so neither re-acquired. Both of the two ways the story names were taken:
on the all-success run the producer received exactly one Commit and no Abandon,
the claim handle ended committed, and the control API reported three holders on
that one claim; on the run whose last holder failed, the same subgraph still
opened the claim exactly once, the producer received Abandon and no Commit, all
three nodes settled failed, and the claim handle ended abandoned.

## Compliance

- The body prescribes mechanism, which the story rules place in decisions: it
  names the template's co-holdership directive and describes the values
  reaching the co-holder "through alias-keyed substitution into the co-holder's
  attribute schema". The compliant text states the need without the delivery
  surface — that downstream nodes can do their work against the same staged
  location the acquirer took, reading its address and its producer-supplied
  details, and that the claim resolves once for all of them.
