---
audit: claim-producer-conformance
artifact: story:claim-producer-conformance
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:22:00Z
---

# A producer author proving their producer with the shipped conformance runner

Supported. A claim producer written against the published protocol was started
three ways on loopback — honest, one rejecting a retried terminal verb, one
serialising a reader behind a writer while advertising staged-async — and the
shipped CLI's conformance verb was pointed at each endpoint; no stack and no
container are needed, so the author needs only their producer and the CLI. All
three ways the story names were taken. The honest producer drew 16 checks, one
`ok` row each, covering Open, Commit, Abandon and Release, the three retried
terminal verbs and the serialization-9b probe, exiting 0. The producer that
rejects a retried terminal verb failed exactly the three retry checks by name
while their first-call counterparts still reported ok, printed how many of the
16 failed, and exited 1 — the report is per check, not one verdict. The
serialising producer failed only the serialization-9b check, whose message
names the forbidden reader-lease pattern, and exited 1.
