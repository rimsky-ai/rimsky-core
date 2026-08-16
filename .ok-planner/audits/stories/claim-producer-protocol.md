---
audit: claim-producer-protocol
artifact: story:claim-producer-protocol
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:55:00Z
---

# A custom producer advertises, opens, drives dispatch and closes its claims, across all four write-semantics

Supported, with the enumerated population covered. A producer written against
the published protocol was started five times: one per advertised
write-semantics value, plus one that always refuses to open. All five are listed
by the control API's producer view carrying the error class each declares, so
the startup advertisement reaches the stack. Four nodes, one per semantics,
each settled fresh. The producer's own call log shows Open arriving with the
selector resolved from the instance parameter, the declared intent, the declared
alias, and the node's opaque data blob byte-for-byte as the template wrote it;
the write claim was closed with a commit, and so was the read-intent claim. The
handle each producer returned reached its node's dispatch — the address, the
scope bytes and a named field of the synthesized payload all arriving in the
node's resolved attributes — and the persisted handles record each producer's
realized semantics, one each of the four, all reaching committed. The producer
that refuses to open settled its node on the error class that producer declares
and returns, rather than on a generic acquisition failure.
