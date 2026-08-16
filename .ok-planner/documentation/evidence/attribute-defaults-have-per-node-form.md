---
trap: attribute-defaults-have-per-node-form
release: d977250c
---
# Evidence set — alongside `defaults.attributes.by_executor.<executor>.<key>` there is a per-node defaults form, since instance-level overrides are documented as per-executor *and* per-node.

Source of the prior: sibling-symmetry — `concept:instance` describing "per-instance per-node attribute fragments" and "per-executor / per-node selectors" while the template exposes only `by_executor`

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-attribute-defaults-have-per-node-form)

# Per-node attribute defaults, at the template and at the instance

## What it ran against

One `rimsky-all-in-one` container from this tree's image set. The run
registers one template per candidate selector under `defaults.attributes` —
`by_executor`, `by_node`, `by_match` — and records which registration accepts.
It then creates one instance per selector under `attribute_overrides` over
`POST /v1/instances`, which is where the surface lives (the CLI's `instance
create` has no flag for it). The two answers side by side are the
measurement.

## What was observed

The template takes one selector; the instance takes three. At the template
level only `defaults.attributes.by_executor` registers. `by_node` and
`by_match` are refused at YAML parse — `field by_node not found in type
spec.TemplateAttributeDefaults` — so an author who writes the per-node form
does not get a validation finding naming the shape, but a parse failure on the
file.

At the instance level, `attribute_overrides` accepted all three: `by_executor`
(HTTP 201), `by_node` (201), and `by_match` (201, once its entries use
`matcher` / `overlay` and a matcher key from the allowed set). So the routing
vocabulary the platform actually resolves attributes with is per-executor,
per-node, and per-match, and only the first of the three can be written once
in the template. 1 check, 0 pass, 1 fail.

Runnables: `src:.ok-planner/experiments/assumption-attribute-defaults-have-per-node-form/` at the stamped commit.
