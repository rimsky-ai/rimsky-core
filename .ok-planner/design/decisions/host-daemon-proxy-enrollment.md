---
decision: host-daemon-proxy-enrollment
---

# The proxy enrolls in the deployment trust domain and pins its control-API trust anchor

## Choice

Under mutual-TLS service auth the host-daemon proxy enrolls with the control API through the same enrollment machinery every bundled service uses, renews its short-lived leaf automatically, and serves the supervisor-facing protocols on a listener that requires and verifies client certificates against the deployment CA — separate from the daemon-facing listener, which keeps its `decision:host-daemon-proxy-tls` posture (server TLS toward daemons, no client certificates). Independently of service-auth mode, the proxy's outbound control-API clients accept the same CA-bundle trust anchor bundled services read (`RIMSKY_CONTROL_API_CA`), honored whenever the control-API URL is HTTPS.

## Rationale

`decision:service-auth-mtls` commits every operator-deployed standing service to enrollment, and the proxy is one; a hand-provisioned static certificate with a short-lived-leaf TTL and a listener that never verifies clients cannot satisfy that commitment, and manual re-minting on the leaf cadence is not an operable posture. Splitting the serving legs keeps the daemon-facing hop non-mutual — the daemon is per-user session tooling, per `decision:host-daemon-proxy-tls` — while making the supervisor-facing hop genuinely mutual. The control-API trust anchor is unconditional because the proxy's control-API calls run in every service-auth mode: a deployment whose control API serves a private-CA certificate must be able to run the proxy without turning on mutual TLS.

## Alternatives

- A long-lived certificate carve-out for the proxy — rejected: abandons the uniform short-lived-leaf posture for exactly one service.
- Sanctioning plaintext on the proxy's service entries under mutual-TLS service auth — rejected: one plaintext internal hop contradicts the trust domain's every-leg claim.
- System trust store only for the outbound control-API client — rejected: locks private-CA deployments out of running the proxy at all.
