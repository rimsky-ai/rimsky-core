---
audit: claim-handoff-durable
artifact: story:claim-handoff-durable
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:22:00Z
---

# A durable claim that outlives its dispatch and releases only on an operator act

Supported, with one wording caveat about which operator act releases. Driving a
stack from this tree through the template surface, the message-send route and
the instance verbs, an acquirer declaring durable lifetime left a single
committed claim-handle row that survived its dispatch, and a co-holder in that
dispatch read the claim by alias. All four ways the story names were taken. A
later dispatch, woken by a message that dispatches the co-holder alone, read
the same address and registered as a third holder on the first dispatch's row:
one row for the scope throughout, unchanged row id, and one Open at the
producer across both dispatches. A competing instance asking for the same scope
was refused while the row stood, so the producer still occupies the scope.
Nothing released the claim on its own. Terminating the instance released
nothing — no Release reached the producer, the row stood, and a later competitor
was still refused — and deleting the terminated instance is the act that
released it: the producer received Release, the row went away, and a competitor
created afterwards claimed the scope and settled fresh. The story's "instance
termination" therefore names an act that does not release by itself; the
release path a user has is the explicit operator action the same sentence
names, applied to the terminated instance.

## Compliance

- The body prescribes mechanism, which the story rules place in decisions: it
  names the co-holdership directive and the internal state machine, promising
  that "the claim handle row persists past auto-terminal (promoted to committed
  rather than reaped)". The compliant text states the need — that the claim
  outlives the dispatch that took it, stays the same claim for later dispatches
  to share, and keeps occupying its scope until the operator gives it up.
- The body names instance termination as a release trigger, which the product
  does not deliver; naming the deliberate operator act without the specific verb
  keeps the promise decidable and true.
