---
story: template-lifecycle
status: as-is
---

# Operator manages template catalog

## Role

As an operator, I can register a workflow definition with rimsky, mark it ready to run, create live instances of it, retire it when I don't want new instances, and remove it once nothing's using it, so that I curate the catalog of workflows my stack offers.

## Capability

Operator-driven template lifecycle: submit, retrieve, deploy, undeploy, instantiate, delete, and pre-flight a template definition through the control-api or CLI.

## Business value

Operators curate the catalog of workflows their rimsky deployment offers, with a controlled lifecycle that prevents bad templates from producing live instances and prevents in-use templates from being removed.

## Acceptance

Through the control-api or the `rimsky template …` CLI, an operator submits a template definition; afterward, the same operator can retrieve it by name or content hash, can mark it deployed and from that point create instances of it that proceed to run, can mark it undeployed and from that point have new instance-creation refused, and can delete it once no instance references it. The operator can also pre-flight a definition through a validation surface and get back findings without the template being persisted.

## Falsifier

Deployed-vs-undeployed state is recorded but not gated on at instance creation (an undeployed template still produces a running instance), OR pre-flight validation persists, OR delete succeeds while live instances reference the template.

## Proof

Executable proof exercising the full lifecycle against the assembled all-in-one stack.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
