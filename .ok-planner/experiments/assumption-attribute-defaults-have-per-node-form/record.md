---
experiment: assumption-attribute-defaults-have-per-node-form
commit: PENDING
---

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
