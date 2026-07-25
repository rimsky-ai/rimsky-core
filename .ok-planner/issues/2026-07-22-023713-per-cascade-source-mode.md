---
issue: per-cascade-source-mode
kind: human
category: scope
artifacts:
  - concept:cascade
  - concept:wait-set
  - concept:node-run
status: verified
opened: 2026-07-22T02:37:13Z
---

# A node can't treat a noisy upstream and a quiet one differently — should it?

In rimsky's node graph, a downstream node re-runs when something upstream changes, and it picks one policy for how to handle a burst of triggers: coalesce them down to the most recent, keep every one in order, or de-duplicate identical ones. That policy is a single setting per node, applied uniformly to *every* upstream feeding it. An author whose node listens to both a chatty upstream (coalesce, please) and an audit-trail upstream (keep every event) can't say so — today they split the node into two nodes purely to hold two policy values, distorting the graph for what is really a per-dependency preference. The question: generalize the policy to vary per upstream source, or bless the status quo?

The gap is real and verified, and the design corpus explicitly punts: the decision that fixed the default policy (`decision:mode-default-most-recent`) calls per-source configurability "undecided" and points at this issue. What makes the generalization non-trivial is the mechanics underneath: when several upstreams feed one pending re-run, one policy currently governs that pending. Give different upstreams different policies and you need an answer for what happens when they collide on the same pending — a precedence question nothing in the current model addresses. There are also two plausible axes to key an override on, and they aren't equivalent: *who sent it* (matches the noisy-vs-quiet motivation) or *what kind of change it was* (a single sender can emit several kinds), and the node's subscription declarations already name both in one place.

## Options

- **Status quo.** Keep one mode per node; document the split-the-node workaround as the supported pattern. Zero new surface; the graph distortion stays.
- **Per-sender override map**, node-wide default plus exceptions. Matches the motivating example; forces the multi-sender collision semantics to be designed.
- **Per-signal-type override map.** A different axis that doesn't match the motivating example on its own; combining both axes needs a precedence rule.
- **Put the mode on the subscription declaration itself** — the one place that already names both sender filter and signal type — collapsing the axes into one declaration site.

The ruling decides: build it at all or close as status quo; if building, which axis; and whether it replaces the single per-node field or layers on as an opt-in.

## Ruling

> Recommended ruling (/recommend-rulings): Do not build the
> generalization. Amend decision:mode-default-most-recent to drop its
> 'undecided' framing and document the mixed-cadence workaround as the
> supported pattern.
>
> Rationale: No use case anywhere in the corpus drives a per-source
> mode, and every proposed axis adds resolution-precedence complexity
> to the cascade walk. If demand materializes, the subscription-entry
> shape is the natural home — a new issue then, with a motivating
> workload.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
