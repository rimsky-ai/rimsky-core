---
audit: host-agent-proxy-tls
artifact: decision:host-agent-proxy-tls
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:16:32Z
---

# Two host-agent hops secured two ways: pinned-root TLS outward, mandatory local-CA mTLS on the loopback

Supported on both hops. Outward, the agent builds its transport credentials from a pinned CA pool loaded from an operator-supplied root and presents no client certificate of its own, so it verifies the proxy as a server and authenticates as the user by carrying the api-key inside the encrypted channel; enabling TLS without a CA root is a startup error. The decision describes this hop as pinned rather than always-on, and it is opt-in, which the flag help text restates in the decision's own words. On the loopback the daemon generates its own certificate authority unconditionally at startup, independently of any deployment auth posture, and it mints no ledger row and asks no permission — the per-spawn credential is a freshly generated secret held in an in-process map with a lifetime bound. Each spawn hands the child that secret through the same three environment variables any bundled service reads to self-enroll, pointed at the daemon's own local enroll endpoint, so the child runs unchanged enrollment code and receives a short-lived leaf from the local authority. Both legs are mutual against that authority: the dispatch dialer presents the agent leaf and pins the local root, and the callback listener requires and verifies a client certificate from the same pool. Coverage spans both hops — the pinned dial trusting the right root and carrying the key, the wrong pin rejected, the CA requirement, the insecure default, and on the loopback the enroll endpoint issuing for a live token and rejecting an unknown one, the readiness probe requiring a handshake, a plaintext port-squatter being retried past before dispatch proceeds over mutual TLS, the spawn environment provisioning, a full forwarded round trip, and the bootstrap token's lifetime and renewal bounds.
