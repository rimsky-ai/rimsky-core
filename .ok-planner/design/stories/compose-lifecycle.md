---
story: compose-lifecycle
status: as-is
---

# Operator drives multi-resource compose manifest

## Role

As an operator declaring a workflow's templates, tags, and instances together as a manifest, I can apply that manifest to a running rimsky and have rimsky reconcile state to match, namespace the resources under a compose-prefixed tag, plan and inspect status before applying, and tear it all down with one command, so that I drive multi-resource changes as one declarative unit.

## Capability

Operator-driven compose lifecycle: up, plan, status, down against a compose manifest, with all resources scoped under a per-project namespace prefix.

## Business value

Operators drive multi-resource changes as one declarative unit, plan before applying, and tear down cleanly without touching unrelated resources.

## Acceptance

An operator writes a compose manifest declaring multiple templates, tags, and instances; the compose-up verb reconciles them into a running rimsky (each resource visible via the standard list surfaces, each carrying a per-project-prefixed tag bound to the manifest's project); the compose-plan verb reports the diff without applying; the compose-status verb reports current state vs. manifest; the compose-down verb removes the project's resources cleanly without touching unrelated ones. No member operation invokes a container-orchestration substrate and no member operation is stubbed.

## Falsifier

Any compose verb returns without performing its reconcile, OR the compose-down verb touches resources outside the project namespace, OR a compose verb shells out to a container-orchestration substrate.

## Proof

Executable proof.
