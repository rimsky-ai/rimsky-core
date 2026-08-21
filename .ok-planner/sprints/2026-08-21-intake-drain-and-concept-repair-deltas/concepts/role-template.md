---
concept: role-template
aliases:
  - bundled role
---

# Role template

## What it is

A role template is a named bundle of permissions. The command-line client expands a role template into one `concept:permission` grant when it creates an api key (see `concept:api-key`). The client carries a set of role templates built into it, spanning full platform access down to a single-action grant. An operator also defines a role template locally. The client expands that role template exactly as it expands a built-in one.

## Purpose

A role template gives an operator a vocabulary the server does not have. The server's only authorization primitive is the per-key grant. An operator names a role and any per-grant overrides, the client assembles the expanded grant, and the client submits the key-creation request. The server stores the expanded grant and records no role identifier.

## Boundaries

A role template owns its own definition, the client-side expansion, and the overrides that add a grant to a role's expansion or remove one from it. It does not own server-side authorization, which is `concept:permission`. It does not own the choice between previewing a change and committing it, which is `concept:dry-run`. A role template an operator defines stays local to that operator: rimsky offers no surface for registering a role with the deployment.

See also: `concept:permission`, `concept:api-key`, `concept:dry-run`, `concept:rimsky`.

## Aliases

- bundled role
