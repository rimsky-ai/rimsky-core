---
decision: run-token-swept
status: as-is
aliases:
  - async-ack-id-correlation-only
---

# Peer identity authenticates the return leg; the ack id is correlation-only

## Choice

The one-time terminal return leg (the async callback to the supervisor, and services' publish-back-to-control-API calls) carries no per-call token: it is authenticated by the mTLS peer identity under `peer_auth: mtls`, and by the trusted-subnet assumption under `none` (see `concept:peer-auth`, `decision:peer-auth-mtls`). The executor-chosen `async_ack_id` is purely a CORRELATION key — which run a callback settles — and never an authenticator (see `concept:executor`). The two ongoing mid-dispatch callback channels (keepalive, attribute writeback) carry a separate per-dispatch bearer token layered underneath peer identity, outside this decision's scope (see `concept:executor`).

## Rationale

Per-call auth on the return leg while nothing authenticates the outbound leg is security theater. And an executor-CHOSEN ack id is not a credential the trusted side issued — treating it as one authenticates the caller against a value the caller picked. Authenticating the connection's peer identity (or trusting the subnet) is the honest boundary; the ack id is what it is, a lookup key.

## Alternatives

- **A per-call scratch/callback token as a return-leg secret** — rejected: it protects only one direction of a two-direction exchange and gives false assurance.
- **A supervisor-issued ack id treated as a bearer secret** — rejected: it re-invents a per-call credential the certificate identity already subsumes under mtls, and adds nothing under none.
