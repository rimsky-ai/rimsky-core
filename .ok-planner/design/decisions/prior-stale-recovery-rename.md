---
decision: prior-stale-recovery-rename
---

# Stale-recovery prior-dispatch disposition

## Choice

The prior-dispatch disposition value `PRIOR_STALE_RECOVERY` (storage form `stale_recovery`) covers the async quiet-period/max-runtime sweep's reap of a stale dispatch, without overspecifying which of the two async deadlines fired.

## Rationale

The recovery semantics are the same regardless of which async deadline fired; the disposition reflects "the prior run was reaped as stale by the deadline sweep" without naming the specific deadline. Sync-dispatch error paths (dial failure, resolve failure, cancellation) are a distinct case: they route through the error-policy retry loop and stamp `retry_after_error`, not `stale_recovery` (see `concept:node-run`).

## Alternatives

Two enum values, one per async detection cause — rejected because the sweep's recovery logic is the same either way; the discriminator would carry no useful information.
