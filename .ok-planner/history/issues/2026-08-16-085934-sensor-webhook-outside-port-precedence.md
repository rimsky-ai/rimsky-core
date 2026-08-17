---
issue: sensor-webhook-outside-port-precedence
kind: audit
category: conflicting
artifacts:
  - concept:service
status: promoted
opened: 2026-08-16T08:59:34Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The webhook sensor resolves its serving port outside the shared precedence every bundled binary follows

The service concept says every bundled out-of-process binary resolves its serving port through one shared precedence — the agent-assigned port first (so the host agent can late-bind it), then the service's own variable, then a built-in default — and that a binary ignoring it cannot be late-bound. Nine of the eleven binaries use the shared helper; the OpenLineage subscriber serves no port; the webhook sensor parses its own variable directly and never consults the agent-assigned port. The ruling routes it through the helper.

## Options

- Route the webhook sensor's port through the shared helper; cost: none beyond the change.
- Carve inbound-listener services out of the invariant; cost: no structural reason exists — executors and producers also serve inbound gRPC and follow the precedence.

The ruling brings the eleventh binary into the rule.

## Ruling

> Generated ruling (/verify-issues): Route the webhook sensor's serving-port resolution through the shared precedence helper the other bundled binaries use, so the agent-assigned port wins when set. Forced by the service concept's unconditional invariant; the sensor's private parse is an oversight, not a distinction. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
