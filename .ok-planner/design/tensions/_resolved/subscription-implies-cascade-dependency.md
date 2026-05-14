---
resolved_by: spec:2026-05-14-subscription-cascade-and-quality-of-life-design
tension: subscription-implies-cascade-dependency
category: implicit-coupling
status: resolved
affects:
  - subscription
  - cascade
  - node
---

# Attribute substitution required `dependencies:`; event substitution didn't

## What was muddy

Pre-2026-05-14 substitution had asymmetric coupling rules:

- `{{deps.X.Y}}` (attribute read) required `X` to appear in the receiver's `dependencies:` list. Without it, the substitution would fail and (depending on `required`) the dispatch would error.
- `{{nodes.X.event.Y.<path>}}` (named-event read) did NOT require `X` in `dependencies:`. The event-lookup table was scanned at substitution time, and the receiver could read events from any emitter in the instance.

The asymmetry was historical: attribute substitution predated subscriptions; event substitution was added under the 2026-05-08 platform-extensions plan F4 with looser coupling.

## Why it mattered

Template authors had to remember "attribute reads need a dependency; event reads don't." The validator's error messages didn't surface the rule explicitly. Migration was guesswork — operators reading an attribute couldn't tell whether the read or the dependency was load-bearing for cascade purposes.

## Resolution

Resolved by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. Both substitution shapes (`{{nodes.X.attribute.Y}}` and `{{nodes.X.event.Y}}`) now auto-subscribe symmetrically via the template-validator's substitution-ref parser. The receiver is automatically added to the subscription-edge inverse map with `topic_kind = "attribute"` (or `"event"`) and `name = Y`. Cascade walks honor the inferred edge identically to an explicit `subscribes:` entry.

The substitution grammar `deps.X.Y` retires in favor of `nodes.X.attribute.Y` to make the auto-subscribe side-effect visually consistent across the two read paths.
