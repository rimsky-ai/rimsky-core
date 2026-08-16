---
audit: verifier-severity-partition
artifact: story:verifier-severity-partition
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:04:03Z
---

# A check's severity label decides whether its failure blocks the commit

Supported. Driven through the public surface against a released-image stack on
three templates that differ only in the severity labels on their declared
checks. Three legs, eight checks, none failing. A failing warning-severity check
did not block: the node settled fresh with no failed run while still counting the
failure and naming it with its kind and severity. Relabelling that same check
error, over the same rows and with nothing else changed, flipped the outcome to
one failed run, no fresh run, and a terminal naming the escalated check — so the
label is what decides. With rows tripping both, the blocking terminal named the
error-severity check rather than the warning-severity one and carried the
non-blocking warning beside the blocking failure in the same record, which is the
partition the story asks for.
