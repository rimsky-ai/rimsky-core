---
issue: story-http-node-split
kind: sprint
category: stories-splits
artifacts:
  - story:http-node
status: retired
opened: 2026-08-01T22:30:10Z
---

# Should the http-node story split into three?

The story for the bundled HTTP executor (the shipped service that calls external HTTP endpoints on a node's behalf) promises three things in one sentence: request/response routing, parking the node when the upstream answers 429 (rate-limited), and configuring which response fields classify an error. The filed worry is that a reader could want any of the three independently, so the story fuses three outcomes.

Re-verification found the story a single well-formed sentence with no prescriptive tail. The three capabilities are not independently adoptable products: they are facets of one executor's configuration surface, and the sentence's benefit clause — integrate with HTTP upstreams without writing a custom executor — is the actual user outcome. Nobody adopts "429 parking" without adopting the executor. The corpus states no per-capability story rule.

## Options

- Split into three stories (routing, 429 parking, error-class config) — three files for facets of one config surface, with no distinct persona per facet.
- Keep bundled and rule the executor's surface one outcome — no identified cost.

The ruling decides whether a bundled executor's capability set is one story or several. Siblings `issue:story-node-admin-split`, `issue:story-instance-lifecycle-split`, and `issue:story-runtime-diagnostics-split` pose the same granularity question and should be ruled consistently.

## Ruling

> Recommended ruling (/verify-issues): keep the story bundled. The deliverable users evaluate is "I can integrate with an HTTP upstream without writing an executor"; routing, 429 parking, and error classification are how that promise cashes out, not three separate promises.
>
> Rationale: the split creates artifacts whose acceptance can't be judged independently — 429 parking is meaningless without the executor around it — which defeats the checkability the story catalog exists for. Flip case: if any facet grows its own separately-adoptable surface (say, the parking behavior becomes a general policy any executor declares), that facet then earns its own story.

Retired at /plan-sprint 2026-08-01-ruled-intake-drain: the accepted ruling is keep-bundled — no corpus change, so nothing to promote.
