---
story: compose-lifecycle
status: as-is
---

# Operator drives multi-resource compose manifest

## Role

As an operator declaring a workflow's templates, tags, and instances together as a manifest, I can apply that manifest to a running rimsky and have rimsky reconcile state to match, namespace the resources under a compose tag, plan and inspect status before applying, and tear it all down with one command, so that I drive multi-resource changes as one declarative unit.

## Capability

Operator-driven compose lifecycle: up, plan, status, down against a `rimsky-compose.yml` manifest, with all resources scoped under a `compose:<project>:` namespace prefix.

## Business value

Operators drive multi-resource changes as one declarative unit, plan before applying, and tear down cleanly without touching unrelated resources.

## Acceptance

An operator writes a `rimsky-compose.yml` declaring multiple templates, tags, and instances; `rimsky compose up <manifest>` reconciles them into a running rimsky (each resource visible via the standard list surfaces, each carrying a `compose:<project>:`-prefixed tag bound to the manifest's project); `rimsky compose plan` reports the diff without applying; `rimsky compose status` reports current state vs. manifest; `rimsky compose down` removes the project's resources cleanly without touching unrelated ones. No member operation invokes infrastructure (docker, kubectl) and no member operation is stubbed.

## Falsifier

Any compose verb returns without performing its reconcile, OR `compose down` touches resources outside the project namespace, OR a compose verb shells out to docker/kubectl.

## Proof

Executable proof.
