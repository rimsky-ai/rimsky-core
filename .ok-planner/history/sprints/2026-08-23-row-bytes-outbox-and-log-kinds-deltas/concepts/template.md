---
concept: template
aliases:
  - canonical-spec
---

# Template

## What it is

A template is the static artifact a consumer registers with rimsky: node definitions, attribute schemas, claim and lock declarations, subscription and cascade-coupling declarations, message-type schemas, publisher declarations, a params schema, late-bind service names, and sub-graph declarations. Its identifier is the content hash of its spec after the deployment's canonicalization. That canonicalization absorbs key order and whitespace and reads the deployment's kind-alias map, so a template's identity is a function of both the spec and the deployment that registers it (see `decision:template-identity-deployment-canonical`). A template passes through a small lifecycle: initial registration, deployment, undeployment, and final deregistration.

## Purpose

Content-addressed identity makes a template a fixed reference that a running instance can hold for as long as it lives. Two specs that canonicalize alike are the same template, and two that differ are not, so re-registering an unchanged spec is idempotent: the registration entry point resolves the incoming spec's hash first and answers with the existing record instead of reporting a conflict. What has to move — which template is the current one — moves by name instead (see `concept:tag`).

## Boundaries

A template owns its spec bytes, its identifier, its lifecycle states, and the registration entry point. That entry point also serves a path that validates a spec without registering it (see `story:template-validate-without-registering`), and it validates every executor, store, and named-lock reference the spec declares, with no setting that relaxes the check (see `decision:template-registration-validation-unconditional`). The template-level late-bind list is the one boundary against that validation: a service the list names is exempt from registration-time existence and schema checks, because its schema arrives from the spawned binary at dispatch (see also `concept:host-daemon-proxy`). The list is part of the spec, so changing it registers a different template.

Deployment routing is out and belongs to `concept:tag`: an instance binds to one template identity at creation, and moving a tag never migrates it. Per-deployment overrides are out (see also `concept:instance`), and so is runtime state (see also `concept:node`). Instance params are cleartext operational data — rimsky masks nothing in them on any surface, and a secret belongs instead in a surface that carries the never-logged, never-returned guarantee (see `decision:secret-at-rest-posture`).

See also `concept:lifecycle-subscriber`.

## Aliases

`canonical-spec`.
