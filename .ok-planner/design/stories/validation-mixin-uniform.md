---
story: validation-mixin-uniform
status: as-is
---

# Service author advertises the validation mix-in from any peer kind

## Story

As a service author, I can advertise the validation mix-in from an executor or publisher — not only from a claim producer — and have my declared validation roles actually honored, so the mix-in works for every peer kind the protocol says it does.

The validation-registry dial walks all peer kinds (stores, executors, publishers) and plumbs the declared validation-supported roles from each kind's capabilities surface — the publisher capabilities message and the executor-observability capabilities message — wiring both identically to claim-producer peers (see `decision:plumb-validation-roles`).

The validation mix-in is uniform across the trust perimeter: a service author chooses the peer kind that fits the service, not the one peer kind whose declared roles happen to be honored.
