---
issue: service-dockerfile-expose-lines-disagree-with-listeners
kind: audit
category: inconsistent
artifacts:
  - concept:service
status: promoted
opened: 2026-08-16T09:15:29Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Four bundled-service Dockerfiles disagree with the ports their binaries open

Four of the ten listening services declare ports their code does not open. A Dockerfile's port declarations enforce nothing, but an operator reads them to learn what a service opens. The postgres claim producer declares an admin port that has no default in code. The filesystem producer declares none, though it carries the same admin field. http-node declares only its gRPC port, though it also binds an HTTP bridge one above it. The two verifiers declare nothing, though both bind default gRPC ports. The subscriber opens no port and declares none. No artifact says the declarations are a maintained surface. The ruling decides whether they become one.

## Options

- Fix the four and settle the admin-port question the same way for both producers; cost: a small surface to keep honest, with no check named.
- Declare the port lines non-maintained and point operators at each service's configuration keys; cost: the images keep drifting.

The ruling decides whether image port declarations are surface.

## Ruling

> Recommended ruling (/verify-issues): Maintain them. Fix the four, declare the admin port on neither producer because it has no default, and add a fitness check that each service image's declared ports equal its code defaults, so the surface cannot drift again.
>
> Rationale: an operator reads an image's port lines first, and the run's port-collision finding shows the defaults matter. A check makes the surface cost nothing to keep. Flip case: if operators are meant to run the images only through configuration that sets every port explicitly, the second option with a one-line disclaimer is honest.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
