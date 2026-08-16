---
assessment: claim-producer-conformance--serialization-probe
subject: story:claim-producer-conformance
way: serialization-probe
release: d977250c
outcome: held
warrant: experiment:claim-producer-conformance
---
# Catching a producer that claims staged-async but serializes internally

A third producer advertised staged-async write semantics while quietly serializing a reader behind a writer on the same scope. The suite failed exactly one check — the serialization probe — and its message named the forbidden reader-lease pattern, so the author is told what the producer did rather than only that a probe disagreed. Every other check still passed and the command exited 1. This is the check that keeps an advertised write semantics honest, which a producer's own tests would not catch because the producer believes its own advertisement.

## Unverified remainder

None: the passing run demonstrates the way as promised.
