---
audit: peer-tls-enforcement
artifact: decision:peer-tls-enforcement
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:28:39Z
---

# Every peer dial site honors the tls key, and the key is writable everywhere it is read

Supported. The config loader accepts a tls key on all five peer entry kinds — claim producer, executor, publisher, validator, data processor — which is a superset of the three the decision names as writable, and it rejects any value outside off and required with an error naming the entry and the accepted values. Every peer dial in the tree resolves its transport credentials and its error-wrapping interceptors from one shared pair of helpers keyed on the mode: the claim-producer dial, the lifecycle dial, the publisher dial, the validation dial, the data-processing dial, the executor gRPC dial, and the observability-handshake dial all pass their entry's configured mode through. The required mode dials verified TLS — system roots unless a deployment CA pool is installed — and the off mode, which an absent key defaults to, returns insecure credentials. The HTTP-bridge executor is the one non-gRPC dial and it honors the mode too, refusing a non-https endpoint under required at client construction. Failures under required are wrapped with the peer name and the mode at both the unary and stream interceptors and in the HTTP client. Tests cover the default, both valid values, and rejection of an invalid value on each of the three named entry kinds; end-to-end scenarios cover verified TLS under required, a loud failure against a plaintext peer under required, and plaintext under off.
