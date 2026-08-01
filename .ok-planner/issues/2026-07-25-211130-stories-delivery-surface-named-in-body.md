---
issue: stories-delivery-surface-named-in-body
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# A dozen stories bake their delivery surface into the story body

The story form is explicit that the delivery surface — which CLI verb, HTTP route, or MCP tool carries a capability — belongs in decisions, not stories: two stories describing the same outcome through different surfaces are one story, so the surface can't be part of the promise. A shifting population of stories names its surface anyway, and re-verification found the set has moved since filing: `story:message-bus` and `story:rimsky-health-check` no longer carry the boilerplate, while `story:event-log-read`, `story:instance-lifecycle`, and `story:lineage-admin` now do, alongside the stable offenders (`story:node-admin`, `story:tag-management`, `story:template-lifecycle`, `story:runtime-diagnostics` with "through the control-api or CLI" tails).

Difficulty splits three ways. For the boilerplate tails the strip is mechanical. For three stories (`story:one-shot-to-terminal`, `story:script-friendly-outcome`, `story:spawned-local-services`) the CLI-invocation surface is woven through the entire promise, so the rewrite needs editorial care to preserve the user outcome once the surface moves to a decision. And two surface-parity stories (`story:mcp-transport`, `story:local-orchestrator-zero-config`) raise the harder question of whether they survive as stories at all or fold into decisions — a retirement-class call. This is one of the three story-population issues that should run as a single joint sweep (`issue:stories-name-rimsky-yml-and-config-keys`, `issue:stories-mechanism-prescription-tail`).

## Options

- **Strip and relocate in the joint stories sweep** — tails strip mechanically; the three CLI-substance stories rewrite to outcome language with their surface commitments landing in decisions; the two parity stories get a survival ruling in the same pass.
- **Amend the story form to allow surface naming where the surface is the point** — breaks the one-outcome-one-story rule the form is built on.

The ruling confirms the forced direction; the parity-story survival call is the sprint's.

## Ruling

> Generated ruling (/verify-issues): strip the delivery surface from
> the offending story bodies inside the joint stories sweep —
> boilerplate tails mechanically, the three CLI-substance stories
> rewritten to outcome language with surface commitments relocated
> into decisions — and rule in the same pass whether
> story:mcp-transport and story:local-orchestrator-zero-config
> survive as stories or fold into decisions. The story-form rule
> admits no surface-is-the-point exemption; only the editing and the
> two survival calls are sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
