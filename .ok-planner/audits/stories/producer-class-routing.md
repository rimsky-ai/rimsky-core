---
audit: producer-class-routing
artifact: story:producer-class-routing
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:06:40Z
---

# A producer's own acquisition error class is routable, with the generic acquire key as fallback

Supported. Driven through the public surface against a released-image stack whose
bundled filesystem producer is pointed at a root that does not exist, so every
acquisition fails with the class that producer declares in its own handshake; one
node template was registered five times changing only its error-class map. Seven
checks, none failing. The producer's class reached the routing surface on the
terminal signal rather than a generic one, and keying that class routed the run.
With no producer-class entry, the generic acquire-family key routed the same
failure, and with both declared the producer-class entry decided rather than the
generic key beside it — fallback below, specific above. Whatever key did the
routing, the emitted signal carried the producer's most specific class. The
validator's vocabulary check agreed: the producer's declared class registered with
no warning, while an undeclared class registered with a warning naming it and the
vocabularies checked.

## Referrals

- referral: the generic acquire-family keys are a documented fallback
  established: the fallback behaves as the story says — measured by routing the
    same producer failure through the generic key with no producer-class entry
    present — while whether it is written down anywhere is not settled by this run
  discipline: documentation
