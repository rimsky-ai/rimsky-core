---
story: script-friendly-outcome
status: as-is
---

# Operator branches on the run's exit-code class

## Role

As an operator integrating one-shot orchestration into a script (CI / build / wrapper), I can branch on the run's outcome class, so that the surrounding script knows whether to proceed, fail, or treat the run as bounded-out.

## Capability

The one-shot verb's exit status encodes three distinct outcome classes — all instances reached success, at least one instance reached failure, and the run was bounded out by an operator-supplied wall-clock limit — plus the conventional Unix code for a SIGINT-interrupted shutdown. A wrapper script reads the exit status and branches on it without parsing the verb's log output.

## Business value

Scripts wrapping the one-shot verb (CI pipelines, build wrappers, scheduled jobs) can compose with it via the conventional Unix exit-status surface — succeed, fail, retry, escalate — without log scraping or output parsing, and the exit-code rule is stable across manifests.

