---
issue: bundled-default-ports-collide-with-core-listeners
kind: audit
category: conflicting
artifacts:
  - decision:image-set-bundled-services
  - decision:network-binding
  - concept:service
status: promoted
opened: 2026-08-16T09:35:05Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Two bundled default ports collide with core listeners

The collisions appear when an operator starts every bundled service and the core stack together at their defaults. The filesystem claim producer's default gRPC port is the supervisor's baked callback port. The host-agent proxy's default agent-facing port is the claude-agent executor's default. Every other default in the table is distinct. No artifact promises that the two populations use distinct ports. The service concept states port precedence, not a table. The ruling decides whether a rule allocates defaults.

## Options

- Add a service invariant that every shipped default port differs from every other one across core listeners and bundled services, and a check that enumerates them; cost: the enumeration.
- Record a default-port allocation decision that reserves a range per population, so future defaults differ by construction; cost: an allocation exercise.
- Move the two colliding defaults and note the core-listener block; cost: operators pinned to the old values break, which the pre-v1 rule accepts.

The ruling decides whether port defaults are governed.

## Ruling

> Recommended ruling (/verify-issues): Record the allocation as a decision. The decision reserves one block for core listeners and one for bundled services. Move the two colliding defaults into their blocks. Add a fitness check that every shipped default falls in its block and that no two coincide.
>
> Rationale: zero-config bring-up is a story the project makes, and a rule plus a check keeps it true when the twelfth service arrives. Moving two ports now is cheap under the pre-v1 rule. Flip case: none worth naming. Moving the two ports alone leaves the next collision to chance.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
