---
audit: openlineage-outbound-bearer
artifact: decision:openlineage-outbound-bearer
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# The OpenLineage subscriber presents an optional outbound bearer token to its receiver

Supported. The subscriber's configuration carries a bearer-token field read from its own namespaced environment variable, defaulting to empty, and the emitter sets an Authorization bearer header on the outbound lineage post only when that field is non-empty — the subscriber acting as a client against a third party's receiver, which is the outbound direction the decision describes. The token is held nowhere else: it does not reach rimsky, it is not the api key any inbound surface checks, and it plays no part in the peer mutual-TLS material, which the subscriber never touches at all since its only rimsky-facing connection is a read-only database pool. Two tests cover both states — the header present with the configured value when a token is set, and no Authorization header at all when it is not.
