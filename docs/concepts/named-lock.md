---
concept: named-lock
definition: |
  A scalar capacity counter at the deployment level. Configured in `rimsky.yml` under `named_locks:`, each entry binds a name to a numeric capacity. Nodes declare lock acquisitions via the `locks:` block; dispatch is gated when the current holder count equals capacity.
proto_symbol: (none)
config_field: rimsky.yml:named_locks
api_surface: (none)
related: [claim, claim-handle]
deprecated_terms: []
---

# Named lock

## Definition

A scalar capacity counter at the deployment level. Configured in `rimsky.yml` under `named_locks:`, each entry binds a name to a numeric capacity. Nodes declare lock acquisitions via the `locks:` block; dispatch is gated when the current holder count equals capacity.

## Why it exists

Some coordination needs aren't shaped like producer-managed state. Common cases: "no more than 5 concurrent claude-agent calls"; "no more than 50 calls to the model API per minute"; "exactly one node may run the deployment trigger at a time."

These don't fit the claim model — there's no underlying state to claim, no scope to canonicalize, no producer to dispatch to. Named locks fill that gap. They're a deployment-level scalar: name plus capacity, configured by the operator, acquired and released by nodes that declare them.

Two modes:

- **`mutex`** — capacity 1. Functionally identical to `counting` with limit 1 but signals the intent.
- **`counting`** — declared capacity. Acquisition succeeds while the current holder count is below capacity.

## How you encounter it

- **Operator config**: the `named_locks:` block in `rimsky.yml`. Each entry is `<name>: { limit: <int> }`.
- **Templates**: each node's `locks:` block lists named locks the node acquires before running. Acquisition is atomic with the run's dispatch and any other declared claims/locks.

## Consumer-visible guarantees

- Named-lock acquisition is atomic with the run's dispatch and any other declared claims. A run that proceeds has all its declared locks; one that fails to acquire any of them backs off and retries on the next tick.
- Acquisitions across multiple locks (and claims) use a deterministic sort order to prevent deadlock under contention.
- Lock release is claimant-guarded: only the supervisor that acquired the lock can release it. Stale orphan sweeps cannot null or delete live ownership.

## Common mistakes

- Treating named locks as semaphores you can signal from outside. They're acquired and released only by the lifecycle of a node's dispatch — there's no manual "release" or "unlock" API.
- Using a named lock where a claim is more appropriate. A named lock has no scope; it can't model "no concurrent writes to *this specific row*." That's what claims are for.

## See also

- [`claim.md`](claim.md)
- [`claim-handle.md`](claim-handle.md)
