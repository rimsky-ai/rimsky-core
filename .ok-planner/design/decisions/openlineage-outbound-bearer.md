---
decision: openlineage-outbound-bearer
aliases:
  - outbound-bearer-token
---

# The OpenLineage subscriber can present an outbound bearer token

## Choice

The bundled OpenLineage subscriber can present an optional outbound bearer token to a secured OpenLineage receiver — rimsky as the CLIENT complying with a third party's authentication requirement. This is the outbound trust boundary (boundary 4): the mirror image of rimsky protecting its own surfaces (see `concept:peer-auth`).

## Rationale

A receiver that gates its ingest behind a bearer token is a normal deployment; without the ability to present one, the subscriber simply cannot deliver to it. The token is the subscriber's outbound credential, held in its own configuration, and is unrelated to rimsky's inbound api-key or the internal mTLS posture.

## Alternatives

- **No outbound auth (assume open receivers)** — rejected: real OpenLineage receivers are commonly authenticated, so an unauthenticated-only client cannot integrate with them.
