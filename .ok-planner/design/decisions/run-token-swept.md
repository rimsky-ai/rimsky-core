---
decision: run-token-swept
status: as-is
aliases:
  - async-ack-id-correlation-only
---

# The per-call run-token is swept; peer identity authenticates the return leg

## Choice

The previously ad-hoc per-call scratch/callback token (`supervisor_id:node_run_id`) and the practice of treating the executor-chosen `async_ack_id` as a credential are both removed. The return leg (the async callback to the supervisor, and services' publish-back-to-control-API calls) is authenticated by the mTLS peer identity under `peer_auth: mtls`, and by the trusted-subnet assumption under `none` (see `concept:peer-auth`, `decision:peer-auth-mtls`). The `async_ack_id` remains purely a CORRELATION key — which run a callback settles — and never an authenticator (see `concept:executor`).

## Rationale

Per-call auth on the return leg while nothing authenticated the outbound leg was security theater. And an executor-CHOSEN ack id is not a credential the trusted side issued — treating it as one authenticated the caller against a value the caller picked. Authenticating the connection's peer identity (or trusting the subnet) is the honest boundary; the ack id goes back to being what it is, a lookup key.

## Alternatives

- **Keep the run-token as a return-leg secret** — rejected: it protected only one direction of a two-direction exchange and gave false assurance.
- **Make the supervisor issue the ack id and treat it as a bearer secret** — rejected: it re-invents a per-call credential the certificate identity already subsumes under mtls, and adds nothing under none.
