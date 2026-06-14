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

## Acceptance

A wrapper script invoking the one-shot orchestrator can distinguish three outcome classes from the orchestrator's exit status: all-instances-success, at-least-one-failure, and a wall-clock bound exceeded (when the operator chose to bound the run). The script can branch on these without parsing log output.

## Falsifier

The orchestrator returns the same exit code for all-success and at-least-one-failure (script can't branch); OR a bounded run that hits its limit returns the same exit code as a clean failure (script can't distinguish bound-killed from failed-and-completed); OR the exit code varies by manifest particulars (script can't write a stable rule).

## Proof

Executable proof — three runs (clean success / one failed instance / a bounded run hitting its limit) verified to produce three distinct exit codes via a wrapper that exits 0, 1, 2 respectively.
