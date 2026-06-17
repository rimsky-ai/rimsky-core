---
decision: prior-stale-recovery-rename
status: as-is
aliases: []
---

# Stale-recovery prior-dispatch disposition

## Choice

The prior-dispatch disposition value `PRIOR_STALE_RECOVERY` (storage form `stale_recovery`) covers both async quiet-period stale and sync RPC-broken stale, without overspecifying which signal failed.

## Rationale

The recovery semantics are the same regardless of which detection signal fired; the disposition reflects "the prior run was reaped as stale" without naming the specific stale-detection mechanism.

## Alternatives

Two enum values, one per detection cause — rejected because the executor's recovery logic is the same either way; the discriminator would carry no useful information.
