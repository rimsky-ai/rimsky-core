---
decision: exposure-no-config
status: adopted
---

# One-shot mode exposed only through its verb

## Choice

One-shot (embedded-stack) mode is exposed only through its dedicated CLI verb. No operator-facing knob in the persistence config block or any other config surface selects the embedded mode.

## Rationale

Simplest usage model. Operators running a deployed rimsky stack cannot accidentally select the embedded mode; the deployed path's config surface stays unchanged.

## Alternatives

- A persistence-config knob selecting embedded mode — rejected: a deployed stack could be flipped into the embedded, non-deployed shape by a config edit, and the deployed config surface grows a field the verb already expresses.
