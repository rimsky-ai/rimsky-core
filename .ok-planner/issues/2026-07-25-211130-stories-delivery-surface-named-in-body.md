---
issue: stories-delivery-surface-named-in-body
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# Eleven stories bake their delivery surface into the story body

The story form is explicit that the delivery surface — which CLI verb, HTTP route, or MCP tool carries a capability — belongs in decisions, not stories: two stories describing the same outcome through different surfaces are one story. Eleven stories name their surface anyway, from a literal route with certificate mechanics (service-enrollment) to "through the control-api or CLI" boilerplate (node-admin, tag-management, template-lifecycle, runtime-diagnostics, rimsky-health-check, message-bus) to stories built around "a single CLI invocation" (one-shot-to-terminal, operator-onboarding, script-friendly-outcome, spawned-local-services).

For most, stripping the surface is mechanical. For three — one-shot-to-terminal, script-friendly-outcome, spawned-local-services — the surface is nearly the entire substance, so the rewriting needs editorial care to preserve the user outcome once the surface moves to a decision. Two adjacent surface-parity stories (mcp-transport, local-orchestrator-zero-config) raise the harder question of whether they survive as stories at all or fold into decisions.

## Options

- A remediation sprint strips surface naming from the eleven, relocating surface commitments to decisions, and rules on the two parity stories' survival in the same pass. Cost: one focused sprint pass with real editorial judgment on five of the files.
- Amend the story form to allow surface naming where the surface is the point — breaks the one-outcome-one-story rule the form is built on.

## Ruling

> Generated ruling (/verify-issues): a sprint strips the delivery surface from the
> eleven story bodies (relocating surface commitments into decisions) and rules whether
> `story:mcp-transport` and `story:local-orchestrator-zero-config` become decisions.
> The story-form rule admits no surface-is-the-point exemption.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
