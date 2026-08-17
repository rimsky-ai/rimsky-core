---
decision: terminal-error-abandoned-as-error-class
---

# `terminal/error/abandoned` is an error-class signal, not a new root signal

## Choice

The held-claim abandon outcome cascades downstream as the signal `terminal/error/abandoned`, shaped uniformly with other error signals as `terminal/error/<class>` where `class=abandoned`. Subscribers can match it via:

- The exact path `terminal/error/abandoned`.
- The wildcard `terminal/error/*` (which already matches every error-class signal).

No new top-level signal root is introduced. The signal taxonomy already supports per-class error signals; abandoned slots into that existing surface.

## Rationale

The taxonomy choice is between:

- **Class form**: `terminal/error/abandoned` (this decision).
- **Root form**: a distinct `terminal/abandoned` root signal.

The class form wins on uniformity. Every failure mode the runtime produces is expressed as a class under the `terminal/error/` root — the taxonomy carries no second shape for a failure, so a subscriber learns the pattern once. Subscribers already use it both ways: a specific class for targeted compensation, or `terminal/error/*` for blanket error handling. Adding abandoned as a new root would force subscribers to learn a second pattern and would split the "I want to react to any failure" use case into two subscriptions.

Abandoned is also semantically an error in the rollback sense: the held work was rolled back, the executor's output is not authoritative, downstream effects predicated on the run succeeding need compensation. It is not a benign termination like `terminal/success` or a deliberate dispatch-internal hold like `transient/park/*`. The error namespace is the right home.

## Alternatives

Distinct `terminal/abandoned` root signal — rejected because it splits the "any failure" use case across two roots and adds a top-level taxonomy entry for a single class. The wildcard surface (`terminal/*`) becomes the only way to match "any failure including abandon," which is broader than most subscribers want.

Reuse an existing error class (e.g., `terminal/error/rollback`) — rejected because abandoned is a specific outcome of the held-claim auto-terminal mechanism, not a general rollback. Distinct class makes the cause legible at the subscriber boundary; subscribers that only care about held-rollback (vs. other rollback flavors that might exist later) can subscribe narrowly.

Suppress the cascade entirely on abandon (no downstream signal) — rejected because downstream subscribers that depended on the held work's success need to know it was rolled back, in order to compensate. Silent rollback is worse than a signaled rollback.
