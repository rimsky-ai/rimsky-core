---
audit: exit-codes
artifact: decision:exit-codes
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The four exit-code classes read against every run-to-terminal path in the CLI

Unsupported: two of the three run-to-terminal paths implement the classes, the third contradicts them. The shutdown coordinator the two self-hosting paths share — the compose one-shot and the self-hosted ephemeral run — maps its four shutdown reasons to exactly the decision's codes: zero for all-instances-success, one for at-least-one-failure, two for the run-timeout, and the conventional interrupt code for a signal, with an unknown reason logged and treated as failure. The remote ephemeral run, which takes the same opt-in timeout flag and is equally a run-to-terminal verb, uses a separate wait-and-cleanup routine that returns one when its timeout expires rather than two, returns zero on interrupt rather than the conventional signal code, and — the sharpest divergence — returns zero once the instance reaches any terminal state, never distinguishing a failed run from a successful one, so an operator branching on the outcome class gets success for a failure. A test pins the zero-on-interrupt behavior, so the divergence is deliberate rather than incidental.
