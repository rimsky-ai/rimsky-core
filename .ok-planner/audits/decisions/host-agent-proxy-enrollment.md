---
audit: host-agent-proxy-enrollment
artifact: decision:host-agent-proxy-enrollment
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# The proxy's deployment enrollment, its split serving legs, and its control-API trust anchor

Supported. The proxy loads its peer identity from the environment through the same enrollment package every bundled service uses, fails closed on a startup error, and starts the automatic leaf-renewal loop that package provides — no proxy-specific certificate path exists anywhere in the binary. When enrollment is active the proxy builds two servers: a peer-facing one carrying the executor, claim-producer, lifecycle-subscriber, and both observability services under the enrolled identity's server options, which require and verify a deployment-CA client certificate, and an agent-facing one carrying only the agent-connection service under server-only credentials that request no client certificate. Tests cover both halves — a CA-issued client certificate accepted and a certificate-less client refused on the peer listener, and the agent listener asserted to carry that one service and nothing else — plus the single-server shape kept when peer auth is off. The control-API trust anchor is independent of that: the proxy builds its outbound HTTP client from the CA-bundle variable the peer-auth package reads, before any enrollment happens, pins it as the sole root when set, and refuses to start when it is set against a non-HTTPS control-API URL; the same client serves both outbound uses, the registration identity check and the instance cache-miss fetch. None of the three rejected alternatives is present.
