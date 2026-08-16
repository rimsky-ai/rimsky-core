---
assumption: attribute-defaults-have-per-node-form
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# alongside `defaults.attributes.by_executor.<executor>.<key>` there is a per-node defaults form, since instance-level overrides are documented as per-executor *and* per-node.

As template author reducing repetition, I would take it that alongside `defaults.attributes.by_executor.<executor>.<key>` there is a per-node defaults form, since instance-level overrides are documented as per-executor *and* per-node.

## Source

sibling-symmetry — `concept:instance` describing "per-instance per-node attribute fragments" and "per-executor / per-node selectors" while the template exposes only `by_executor`

## What a run would observe

declare a `defaults.attributes.by_node.<node>.<key>` block in a template and see whether registration accepts it.

## Measured

`.ok-planner/experiments/assumption-attribute-defaults-have-per-node-form` —
built for this run — registered one template per candidate selector under
`defaults.attributes` and created one instance per selector under
`attribute_overrides`, against one `rimsky-all-in-one` from this tree's image
set.

The template takes one selector and the instance takes three. At the template
level only `by_executor` registers; `by_node` and `by_match` are refused at
YAML parse with `field by_node not found in type
spec.TemplateAttributeDefaults`, so the author gets a parse failure on the
file rather than a validation finding that names the shape. At the instance
level `attribute_overrides` accepted `by_executor`, `by_node`, and `by_match`,
each returning HTTP 201.

The asymmetry is exactly the one the prior infers from and expects to be
resolved the other way: rimsky resolves attributes per-executor, per-node, and
per-match, and only the per-executor form can be written once in the template.
An author factoring out repetition across the nodes of one executor can do it;
an author factoring out per-node defaults must repeat them on every instance
creation. 1 check, 0 pass, 1 fail.
