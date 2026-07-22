---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: blocked-vs-errored-routing
category: unclear
status: open
affects:
  - error-policy
  - executor
  - lifecycle-handler
---

# `Blocked` vs `Errored` distinction is semantic, not structural — common confusion source

## What is muddy

Both `Blocked` and `Errored` are terminal events. They differ in intent:

- **`Blocked`** — "I produced output but explicitly chose not to claim success" (e.g. low-confidence routing). Typically routed via `on_executor_blocked` to a downstream reviewer/router. Does NOT increment the retry counter.
- **`Errored`** — true failure with an `error_class`. Routed via `on_executor_errored` and the error-types policy chain. Increments the retry counter.

An executor author can choose either for "I'm not happy with my output" — the difference is whether the dispatch counts as a retryable error or as a routing signal. The protocol does not constrain the choice; the distinction is convention.

## Why it matters

A misclassification produces unintended behavior: emitting `Errored` for what should be a routing signal triggers retry loops; emitting `Blocked` for what should be a true failure prevents retry. Diagnosis requires understanding both semantics.

## Resolution candidates (do NOT pick)

- Strengthen the convention with explicit guidance in `docs/protocols/executor.md`.
- Provide a third terminal variant for "review-needed" that's neither blocked nor errored.
- Tabulate the retry-counter-increment behavior per terminal in `docs/concepts/error-policy.md`.

## Evidence

- `_discover/error-policy-retry-loop-cap.md` Observations bullet "Blocked vs Errored".
- `_discover/2026-05-10-executor-streamed-execute.md` Description.

