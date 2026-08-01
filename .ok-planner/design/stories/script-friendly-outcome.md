---
story: script-friendly-outcome
status: as-is
---

# Operator branches on the run's outcome class

## Story

As an operator integrating one-shot orchestration into a script, I can branch on the run's outcome class — all succeeded, something failed, or the run was bounded out — without parsing log output, so that the surrounding script knows whether to proceed, fail, or escalate.
