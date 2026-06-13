---
decision: peer-tls-enforcement
status: as-is
---

# The tls key works, for every peer kind

## Choice

The `tls` config key is writable on executor, store, and publisher peer entries, and every peer dial site honors the configured mode — the runtime's peer clients (store, publisher, data-processing, validation), the executor dial, and the observability-handshake dial: `required` dials with verified TLS against system roots; `off` (the default) stays plaintext; failures under `required` name the peer and the mode (see `story:peer-tls-enforced`).

## Rationale

A security-shaped config key that is accepted and ignored manufactures false confidence exactly where it is costliest; a key only one peer kind can even write would leave the other dial sites unconfigurable.
