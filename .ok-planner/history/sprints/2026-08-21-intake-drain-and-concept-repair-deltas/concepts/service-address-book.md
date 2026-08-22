---
concept: service-address-book
---

# Service address book

## What it is

The service address book is the deployment's shared, persisted catalog of its declared dispatch peers: each executor name and each claim-producer store name, mapped to the endpoint that answers it. The control plane publishes the deployment's declarations into the catalog at startup. Every supervisor resolves an executor name or a store name against the catalog read-through, behind a short-lived cache, rather than holding a boot-time snapshot of its own process configuration.

## Purpose

The address book makes dispatch routing shared state instead of per-process state. A supervisor never acts on its own process configuration. It resolves every name against the one catalog the control plane published, so every supervisor in the deployment reads the same service list. Every supervisor therefore resolves every declared name, and no queued run waits unclaimable because no supervisor's private accept-list matched its executor or its stores. A name that resolves nowhere fails loudly inside a claimed dispatch instead of stalling silently in the queue. Universal reachability is the deployment's own requirement: every supervisor can reach every declared executor and every declared store. Rimsky partitions reachability nowhere by implication.

## Boundaries

The address book owns the name-to-endpoint catalog for executors and claim-producer stores, the lifecycle that publishes that catalog at startup, and read-through resolution against it. It does not own an instance-scoped binding to a late-bound service, which is `concept:host-agent-proxy`. It does not own dispatch, which is `concept:supervisor`. It does not own claim state, which is `concept:claim-producer`. It does not own how registration validates a template's declared references, which is `concept:template`.

See also: `concept:supervisor`, `concept:executor`, `concept:claim-producer`, `concept:control-api`, `concept:host-agent-proxy`, `concept:template`.
