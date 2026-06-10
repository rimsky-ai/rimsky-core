---
story: instance-lifecycle
status: as-is
---

# Operator manages instance runtime lifecycle

## Role

As an operator, I can create a live instance of a deployed template, watch its progress, pause and resume it, force-terminate it when it's wedged, and remove its record once it's done, so that I drive an instance's runtime existence and intervene when something goes wrong.

## Capability

Operator-driven instance lifecycle: create, observe, pause, resume, force-terminate, delete an instance through the control-api or CLI.

## Business value

Operators can drive an instance's runtime existence and intervene cleanly when something goes wrong — including wedged dispatches awaiting callbacks that never arrive — without bypassing the real lifecycle path.

## Acceptance

Through the control-api or `rimsky instance …` CLI, an operator creates an instance of a deployed template; afterward, the supervisor begins dispatching its nodes. The operator can pause the instance — the supervisor stops claiming new dispatches against it — and resume — the supervisor picks it up again. The operator can force-terminate an instance whose node is wedged awaiting an executor callback that never arrives; the wedged node-run transitions to a terminal state through the real lifecycle path (not by a direct write to the row), the main run-scope closes, and the operator can then delete the instance record. Deleting a non-terminal instance is refused.

## Falsifier

Pause is recorded but the supervisor keeps dispatching against the instance, OR force-terminate writes a row but doesn't propagate to the in-flight node-run, OR delete succeeds non-terminal.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
