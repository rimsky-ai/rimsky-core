---
decision: service-tls-enforcement
---

# The tls key works, for every service kind

## Choice

The service TLS config key is writable on executor, store, and publisher service entries, and every service dial site honors the configured mode — the runtime's service clients (store, publisher, data-processing, validation), the executor dial, and the observability-handshake dial: the required mode dials with verified TLS against system roots; the off mode (the default) stays plaintext; failures under the required mode name the service and the mode (see `story:service-tls-enforced`).

## Rationale

A security-shaped config key that is accepted and ignored manufactures false confidence exactly where it is costliest; a key only one service kind can even write would leave the other dial sites unconfigurable.

## Alternatives

- Honor the key on one service kind and accept-but-ignore it elsewhere — rejected: an operator who set it believes those dials are verified when they are not.
- Restrict the key to the service kinds that honor it — rejected: leaves the remaining dial sites with no way to require TLS at all.
