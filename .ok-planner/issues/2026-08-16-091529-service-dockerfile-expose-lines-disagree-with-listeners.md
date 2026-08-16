---
issue: service-dockerfile-expose-lines-disagree-with-listeners
kind: audit
category: inconsistent
artifacts:
  - concept:service
status: verified
opened: 2026-08-16T09:15:29Z
---

# Four bundled-service Dockerfiles disagree with the ports their binaries open

A Dockerfile's port declarations are documentation, not enforcement, but an operator reads them to learn what a service opens. Four of the ten listening services disagree with their code: the postgres claim producer declares an admin port that has no default in code, the filesystem producer declares none though it carries the same admin field, http-node declares only its gRPC port though it also binds an HTTP bridge one above it, and the two verifiers declare nothing though both bind default gRPC ports. The subscriber declares nothing correctly. No artifact says the declarations are a maintained surface. The ruling decides whether they become one.

## Options

- Fix the four and settle the admin-port question the same way for both producers; cost: a small surface to keep honest, with no check named.
- Declare the port lines non-maintained and point operators at each service's configuration keys; cost: the images keep drifting.

The ruling decides whether image port declarations are surface.

## Ruling

> Recommended ruling (/verify-issues): Maintain them — fix the four, declare the admin port on neither producer (it has no default), and add a fitness check that each service image's declared ports equal its code defaults, so the surface cannot drift again.
>
> Rationale: an operator's first read of an image is its port lines, and the run's port-collision finding shows the defaults matter; a check makes the surface cost nothing to keep. Flip case: if the images are meant to be run only through configuration that sets every port explicitly, the second option with a one-line disclaimer is honest.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
