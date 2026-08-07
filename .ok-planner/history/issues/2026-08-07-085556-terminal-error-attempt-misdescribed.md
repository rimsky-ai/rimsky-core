---
issue: terminal-error-attempt-misdescribed
kind: human
category: inconsistent
artifacts:
  - concept:signal
status: answered
opened: 2026-08-07T08:55:56Z
github: https://github.com/rimsky-ai/rimsky-core/issues/79
---

# TerminalErrorPayload.attempt misdescribes its value and duplicates retries_so_far

Question: is `TerminalErrorPayload.attempt` always equal to `retries_so_far` on the terminal-error payload, and is that duplication intentional?

Answer: yes on both counts — `concept:signal`'s Invariants already settle this: "The policy-evaluation cursor is a single per-dispatch retry counter... Emitted error and retry signals report this counter: the terminal-error payload's `attempt` and `retries_so_far` fields both carry the counter's value at emission — the number of retries completed before the signal — and the transient-retry signal's type-path embeds the same value." Re-verified against HEAD: every live `BuildTerminalErrorSignal` call site (`lib/runtime/runner_error_policy.go#387,505`, `lib/runtime/state_propagation.go#27`, `lib/runtime/signal_for_terminal.go`, `lib/runtime/runner_terminal.go#364`, `lib/runtime/held_cascade_defer.go#305`) still passes the same value for both parameters. The corpus documents this as deliberate, not as a bug — the terminal-error payload's `attempt` is the retries-completed counter by design, distinct from the retry signal's execution-ordinal use of the same field name.
