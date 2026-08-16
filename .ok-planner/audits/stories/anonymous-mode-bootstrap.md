---
audit: anonymous-mode-bootstrap
artifact: story:anonymous-mode-bootstrap
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:39:20Z
---

# A fresh deployment is open to an operator with no credentials, and closes the moment the first admin key is minted

Supported. Two fresh all-in-one deployments were driven through the control API
and the CLI: one on the zero-config default, one configured for mTLS peer auth
so the enrollment and CA-root routes are mounted. All 85 ruled control-API
routes were enumerated from the surface extraction and exercised — 83 against the
default stack, and the two CA-gated ones against the mTLS stack. With no
credential presented, none of the 83 was refused (34 answered 2xx, 12 returned
400 for deliberately empty bodies, 37 returned 404 for deliberately absent
identifiers), and a complete operator lifecycle — register a template, deploy
it, create an instance, read it, terminate it — ran end to end unauthenticated.
Service enrollment was the single refusal, 403 with a message naming the missing
authenticated principal, while the CA root answered 200. Minting the first admin
key through the CLI printed its plaintext once and closed anonymous mode: the
same 83-route sweep then returned 401 on 82, the sole survivor being the
liveness probe. The key restored every checked action, enrollment included, and
the CA root stayed open.
