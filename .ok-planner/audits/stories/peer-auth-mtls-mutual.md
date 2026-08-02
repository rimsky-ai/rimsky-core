---
audit: peer-auth-mtls-mutual
artifact: story:peer-auth-mtls-mutual
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095814-peer-auth-mtls-forward-legs-not-tied-to-switch
---

# Operator enables mutual TLS on internal service traffic by one config flip

Unsupported. The deployment certificate authority, the enrollment exchange, and the supervisor's asynchronous callback return leg are all genuinely gated by the peer-authentication switch and correctly mutually authenticated when it is on. But the switch does not, by itself, put transport encryption or client certificates on the wire for the forward dispatch legs the story names: checked all six forward dial sites, each gates transport security on its own independently-defaulted-off setting that nothing ties to the peer-authentication switch, demonstrated directly by a test showing a client with a valid mutual-TLS identity still dialing in the clear under the default. Every test that exercises genuine mutual-TLS dispatch sets both switches explicitly, confirming two knobs are required, not one. The control API's own inbound listener never enables transport security under this mode either, and the one test-harness option built to exercise a full bundled-service enrollment-and-dispatch flow under this posture is defined but never invoked by any test.
