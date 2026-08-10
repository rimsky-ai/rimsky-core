---
audit: producer-class-routing
artifact: story:producer-class-routing
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Routing the acquisition error class the producer named, with the generic key as fallback

Supported. Against an all-in-one deployment whose filesystem-backed claim
producer was pointed at a missing root, every acquisition failed with the class
that producer declares in its capabilities handshake, and the emitted terminal
signal carried that class rather than a generic one. Five templates differing
only in their error-class-to-action map were driven over that one failure: the
one keying the producer's class to pass settled the node fresh while its generic
acquire-family key said give up; the one with no producer-class entry and the
generic key set to pass settled fresh too, so the generic key is the fallback;
the one keying the producer's class to give up with both generic keys set to
pass settled the node failed, so the producer's class outranks the generic key
beside it. Registration accepted the producer's declared class without a
warning and accepted an undeclared class with a warning naming it.

## Compliance

The capability clause names a template key ("the template's error-types
declaration"), a delivery-surface choice the story rules place in `decisions/`,
and calls the generic fallback "documented", which is a claim about the
documentation rather than about what the user can do; the compliant text is "I
can route a producer-declared acquisition error class, and rely on the generic
acquire-family keys as a fallback".
