---
audit: peer-auth-mtls
artifact: decision:peer-auth-mtls
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095814-peer-auth-mtls-forward-legs-not-tied-to-switch
---

# Internal service auth is optional mutual TLS, default off

Unsupported on the Choice's central claim that flipping the switch alone covers both peers of every internal connection, uniformly across both dispatch transports and services' publish-back calls. The default-off posture, the certificate authority with its encrypted-at-rest key, the enrollment exchange, and the asynchronous callback return leg are all real and correctly gated. But every forward dispatch dial site — checked all six — gates transport security on a separate, independently-defaulted-off per-peer setting that nothing ties to the switch, proven directly by a test showing a valid mutual-TLS identity still dialing in the clear under the default; every test exercising genuine end-to-end mutual-TLS dispatch sets both settings together. The control API's own inbound listener never enables transport security under this mode, and no test exercises a real bundled service enrolling and dispatching end-to-end under this posture.
