---
audit: producer-error-passthrough
artifact: story:producer-error-passthrough
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:42:46Z
---

# An operator reads a failing producer's own class and message in what the operation returns

Supported. Driven through the public surface against a released-image stack
wired to two claim producers built for the experiment against the published
producer protocols, each refusing its release verb with a different declared
error class. Fifteen checks, none failing. Retiring the asset each producer
holds failed rather than reporting success, and each failure came back with the
producer's own error class, the producer's own message naming the claim it
refused to drop, the name of the producer that failed, and the verb it was
running. The two producers yielded two different classes, so the class follows
the producer and is not a rimsky constant; the status distinguished a producer
rejection from a rimsky internal error; the operator CLI exited non-zero and
repeated the producer's message; and both assets remained in place, matching
the refusal.

## Compliance

- The body names the delivery surface — "during an API-triggered operation" and "in the API response" pin the capability to one surface, which decisions own; the compliant capability is reading the producer's error class and message in what the failing operation returns, whichever surface the operator drove it from.
