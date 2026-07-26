---
issue: conflict-held-cascade-firing-gate
kind: audit
category: conflicting
artifacts:
  - concept:cascade
  - concept:signal
  - concept:auto-terminal
status: verified
opened: 2026-07-25T21:11:30Z
---

# The signal concept denies a terminal signal that the held transition demonstrably emits

When a run finishes while holding a held-subgraph claim, it settles as `held` — its terminal outcome is real but its downstream consequences are deferred until the subgraph resolves. The cascade concept's firing gate admits only settling signals, and the signal concept flatly says the running-to-held transition "emits NO terminal signal." Yet the cascade concept's own held-defer bullet and the auto-terminal concept both say the walker fires immediately at the held terminal, filtered to subgraph co-members — which would be impossible if no admissible signal existed.

The code resolves it: the held transition builds a genuine `terminal/success` or `terminal/error/<class>` signal — the exact kinds the firing gate admits — and emits it through the cascade walker with a receiver-side filter restricting delivery to holding-subgraph co-members (`code:lib/runtime/runner_terminal.go`, `code:lib/runtime/runner_error_policy.go`). Non-members get the same signal later, when auto-terminal resolves the subgraph. So the mechanism is a filter, not a suppression; the signal concept's "emits NO terminal signal" is the only false sentence in the set.

## Options

- Correct `concept:signal`: the held transition emits its terminal signal, receiver-filtered to co-members, with non-member delivery deferred. Cost: sprint work only.
- Invent a distinct held-signal kind — unnecessary; the existing settling kinds already carry the mechanism.

## Ruling

> Generated ruling (/verify-issues): amend `concept:signal` to state that the
> running-to-held transition emits the run's terminal signal filtered to holding-subgraph
> co-members (deferred, not suppressed, for everyone else), matching the code and the
> held-defer text in `concept:cascade` and `concept:auto-terminal`.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
