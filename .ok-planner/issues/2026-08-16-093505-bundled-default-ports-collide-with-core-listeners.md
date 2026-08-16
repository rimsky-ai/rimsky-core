---
issue: bundled-default-ports-collide-with-core-listeners
kind: audit
category: conflicting
artifacts:
  - decision:image-set-bundled-services
  - decision:network-binding
  - concept:service
status: verified
opened: 2026-08-16T09:35:05Z
---

# Two bundled default ports collide with core listeners

Bringing every bundled service and the core stack up together at defaults hits exactly two collisions: the filesystem claim producer's default gRPC port is the supervisor's baked callback port, and the host-agent proxy's default agent-facing port is the claude-agent executor's default. Everything else in the default table is distinct. No artifact promises cross-population distinctness — the service concept states port precedence, not a table. The ruling decides whether defaults are allocated by rule.

## Options

- Add a service invariant that every shipped default port is distinct across core listeners and bundled services, with an enumerating check; cost: the enumeration.
- Record a default-port allocation decision with reserved ranges per population, so future defaults are distinct by construction; cost: an allocation exercise.
- Move the two colliding defaults and note the core-listener block; cost: breaks operators pinned to the old values (pre-v1 acceptable).

The ruling decides whether port defaults are governed.

## Ruling

> Recommended ruling (/verify-issues): Record the allocation as a decision — one block for core listeners, one for bundled services — move the two colliding defaults into their blocks, and add a fitness check that every shipped default falls in its block and no two coincide.
>
> Rationale: zero-config bring-up is a story the project makes, and a rule plus a check keeps it true when the twelfth service arrives; moving two ports now is cheap under the pre-v1 rule. Flip case: none worth naming — the minimal move alone leaves the next collision to chance.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
