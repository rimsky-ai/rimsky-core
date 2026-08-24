---
concept: error-policy
---

# Error policy

## What it is

An error policy is a template-level routing surface that maps each error class to one runtime action, drawn from a closed action vocabulary. The runtime looks the action up by class when a dispatch reaches a terminal error. A per-node retry cap bounds the retries within a single dispatch. Error routing is the decision surface for every operator-side error: an executor-emitted failure and a runtime acquisition failure alike arrive carrying an error class, and both match against the same per-class map. A reserved class-name namespace covers the runtime's own acquisition failures, one synthetic class per failure kind, so an operator who wants a retry on acquisition declares a policy keyed by the relevant class. A producer may name a more specific class on an acquisition failure, drawn from the vocabulary the producer declares, and the lookup then falls back from that exact class to the synthetic family class (see `decision:acquire-prefix-fallback`); the emitted signal still carries the most specific class. With no matching entry at all, the runtime gives up and names the unknown class as the reason: failing fast is the default, and retrying is opt-in. Infra-class dispatch faults bypass the map entirely and answer to the supervisor-side retry cap instead (see `concept:terminal-resolution`).

Registration range-checks policy keys against the vocabularies the node's services declare.

## Purpose

Different errors warrant different responses, and an error policy lets the template author say which without writing code. The declarative policy spares every executor from reinventing retry and cascade semantics, lets rimsky bound a runaway retry loop uniformly, and puts an executor-emitted error and a runtime acquisition failure under one routing surface.

## Boundaries

An error policy owns the closed action vocabulary, the per-class action lookup over both executor-emitted failures and acquisition failures, the per-dispatch retry budget and its cap, and the closed retry-backoff vocabularies with the registration gate that checks them. It does not own the signal type-path taxonomy (see `concept:signal`), the firing of a cascade (see `concept:cascade`), or the stitching from a terminal event to a producer verb (see `concept:terminal-resolution`).

see also: `signal`, `frame`, `terminal-resolution`
