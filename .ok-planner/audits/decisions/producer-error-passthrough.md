---
audit: producer-error-passthrough
artifact: decision:producer-error-passthrough
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:37:40Z
---

# Producer error class and message survive into the HTTP response body under a distinguishing status

Supported. The control-api error writer inspects the error chain for a producer-call error and, when it finds one, hands off to a dedicated writer that puts the producer's name, its error class, and its message into the response body as separate keys rather than flattening them into one string. The status distinguishes the two cases the decision cares about: a producer that rejected the request answers unprocessable-entity, chosen from the transport codes that mean the producer said no, while any other producer failure answers bad-gateway — and both are distinct from the internal-error status a rimsky-side failure still gets. A dedicated test file covers all five branches: the bad-gateway case carrying class and message, the unprocessable-entity rejection, a producer failure with no class declared, recognition through a wrapping error, and internal errors passing through unchanged.
