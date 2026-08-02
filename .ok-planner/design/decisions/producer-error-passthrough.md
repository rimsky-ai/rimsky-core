---
decision: producer-error-passthrough
---

# Producer errors cross the HTTP boundary intact

## Choice

The control-api error writer recognizes producer-error types: the producer's error class and message are carried in the response body, under a status distinguishing producer rejection from rimsky internal error (see `story:producer-error-passthrough`).

## Rationale

The error body is the one document every operator and agent reads; discarding a structured class into a bare internal-error response wastes diagnosis the producer already did.

## Alternatives

- Collapse producer failures into a generic internal-error response — rejected: the class and message the producer produced are lost exactly where the operator reads them.
