---
story: instance-lifecycle
status: as-is
---

# Operator manages instance runtime lifecycle

## Story

As an operator, I can create a live instance of a deployed template, watch its progress, pause and resume it, force-terminate it when it's wedged, and remove its record once it's done, so that I drive an instance's runtime existence and intervene when something goes wrong.

Operator-driven instance lifecycle: create, observe, pause, resume, force-terminate, delete an instance through the control-api or CLI.

Operators can drive an instance's runtime existence and intervene cleanly when something goes wrong — including wedged dispatches awaiting callbacks that never arrive — without bypassing the real lifecycle path.
