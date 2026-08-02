---
story: compose-lifecycle
---

# Operator drives multi-resource compose manifest

## Story

As an operator declaring a workflow's templates, tags, and instances together as a manifest, I can apply that manifest to a running rimsky and have rimsky reconcile state to match, namespace the resources under a compose-prefixed tag, plan and inspect status before applying, and tear it all down with one command, so that I drive multi-resource changes as one declarative unit.
