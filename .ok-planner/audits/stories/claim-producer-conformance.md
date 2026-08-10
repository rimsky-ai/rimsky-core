---
audit: claim-producer-conformance
artifact: story:claim-producer-conformance
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# A producer author proves their producer with the shipped conformance verb

Supported. A claim producer written against the published protocol was started
three ways on loopback and `rimsky conformance claim-producer` was pointed at
each. Against the honest producer the suite ran 16 checks, printed one `ok` row
per check — including Commit, Abandon, Release, the three retry rows and the
serialization-9b row — and exited 0. Against a producer that rejects a retried
terminal verb, exactly the three retry rows failed, their first-call
counterparts still reported ok, the run printed `3/16 checks failed`, and the
command exited 1. Against a producer that blocks a reader while a writer holds
the byte-equal scope while advertising staged-async, only the serialization-9b
row failed, naming the reader-lease pattern, and the command exited 1. The
author needs their own producer and the CLI; no stack is involved.

## Compliance

The body names an internal probe identifier ("the serialization-9b probe") and
prescribes the report's format ("reporting pass / fail per check"). A story owes
the need, not the instrument's internals or its output shape. Compliant text:
"As a claim-producer author shipping a custom producer, I can run rimsky's
conformance suite against my producer and learn, per requirement, which ones it
meets and which it breaks — including the ones a producer can appear to meet by
serialising work it claims to do concurrently — so that I find out before I ship
rather than after."
