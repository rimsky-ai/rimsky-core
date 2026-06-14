---
decision: exposure-no-config
status: adopted
---

# exposure-no-config

## Choice

One-shot mode is exposed only via the new verb. No operator-facing knob is added to the persistence config block or any other operator-facing config surface.

## Rationale

Simplest usage model. Operators running a deployed rimsky stack cannot accidentally select the embedded mode; the simplest deployment path stays unchanged.
