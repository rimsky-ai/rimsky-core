---
audit: producer-error-passthrough
artifact: story:producer-error-passthrough
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# The producer's class and message survive the response boundary

Supported. Against an all-in-one deployment wired to two claim producers whose
release verb rejects with a class each names, one durable asset was materialized
against each and the operator asked to retire both. Each retire returned HTTP
422 with a body carrying the producer's error class, the producer's own message
naming the claim it refused to drop, the name of the producer that failed, and
the verb it was running. The two producers produced two different classes in the
two responses, so the class follows the producer rather than being a rimsky
constant, and 422 rather than 500 separates the producer's rejection from a
rimsky internal error. Nothing was read from the deployment's logs, and both
assets remained listed because the producer refused the release.

## Compliance

The capability clause names the delivery surface ("during an API-triggered
operation", "in the API response"), which the story rules place in `decisions/`,
and it calls the same peer both "store" and "claim producer" in one sentence;
the compliant text is "As an operator whose claim producer fails during an
operation I triggered, I can read the producer's error class and message in the
response, so that I can fix the underlying problem from the response alone
instead of reading rimsky's logs."
