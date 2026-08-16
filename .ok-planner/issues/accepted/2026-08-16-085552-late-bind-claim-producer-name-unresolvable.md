---
issue: late-bind-claim-producer-name-unresolvable
kind: audit
category: conflicting
artifacts:
  - story:host-agent-late-bind-all-protocols
  - concept:host-agent-proxy
  - concept:service-address-book
status: verified
opened: 2026-08-16T08:55:52Z
---

# A late-bound claim producer cannot be found by the same name that late-binds an executor

The host agent late-binds local binaries: a template names a service, and the deployment's proxy entry routes the dispatch to a binary the agent spawns. A story promises this works identically for the two implementable protocols. For executors it does. For claim producers, a template that names the late-bind service the same way fails at dispatch with an unresolved-producer error and the binary is never spawned; only naming the deployment's configured proxy entry directly works. The cause is a one-line asymmetry: the executor path resolves the proxy name through the full chain (in-process registry, then the address book where configured peers are published); the claim-producer path ends in a bare in-process lookup that never sees the address book. The ruling routes the second path through the same chain.

## Options

- Resolve the claim-producer late-bind proxy name through the address book like the executor path; cost: none beyond the change.
- Document the divergence and refuse the late-bind spelling for producers at registration; cost: contradicts the story and the address-book concept ("every supervisor can resolve every declared name") with no rationale on record.

The ruling makes the two paths resolve alike.

## Ruling

> Generated ruling (/verify-issues): Give the claim-producer late-bind path the same proxy-name resolution the executor path has — in-process registry first, then the address book — so a declared proxy entry resolves for both protocols. Forced by the story's "identically across both" and the address-book concept's every-declared-name invariant; the divergence is an implementation gap in one of two parallel paths, not a recorded choice. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
