---
audit: peer-auth-mtls
artifact: decision:peer-auth-mtls
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:34:05Z
---

# A two-mode peer-auth switch, default off, turning every internal leg mutual-TLS when on

Supported. The deployment-level switch parses to exactly two values and defaults to the off value when unset, so an absent setting leaves every internal dial plaintext and costs local dev and the containerised suites nothing; per-peer TLS defaults follow the switch rather than being set separately. With the switch on, the control plane generates or loads a per-deployment CA whose private key is stored encrypted under an operator-supplied environment key using authenticated symmetric encryption, and startup fails closed at configuration load when that key is absent, not base64, or the wrong length. Enrollment requires an authenticated api-key principal, refuses anything else, and returns the leaf certificate, its private key, the CA root, and the expiry; clients renew at two-thirds of the lifetime against an injected clock and hot-swap the certificate in place. Checked all four legs the decision names for coverage: the gRPC dial builds a client config that presents the enrolled leaf and pins the deployment root, the HTTP dispatch bridge installs that same client config on its transport, the supervisor's async-callback listener requires and verifies a client certificate from the deployment CA, and the control-API listener verifies a presented client certificate — allowing the operator paths that carry only an api-key — which is what services publishing back present. Tests cover the mutual handshake, an impostor CA rejected, a missing client certificate rejected, the off mode leaving both configs nil and the wire plaintext, a required peer speaking plaintext failing loudly, enrollment against both plaintext and pinned-root control APIs, and an end-to-end stack flipped to mutual TLS in one setting.
