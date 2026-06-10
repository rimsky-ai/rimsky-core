---
story: verifier-severity-partition
status: as-is
---

# Template author distinguishes warning vs error

## Role

As a template author declaring data-quality checks, I can label a check `warning` or `error` and have the verifier honor the partition — failing-warning is non-blocking (the run still succeeds), failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.

## Capability

Severity partition in verifier nodes: `severity: warning` is non-blocking; `severity: error` (and any non-`warning` string today) is blocking. Both observable through the runtime surface.

## Business value

Template authors distinguish observed-but-tolerated quality issues from blocking ones without re-architecting their pipeline; runtime honors the partition consistently.

## Acceptance

With a template whose verifier node carries one `severity: warning` failing check and one `severity: error` passing check against an in-bounds dataset, the dispatch reaches terminal success and the observability surface records the failed check as warning. A second dispatch against an out-of-bounds dataset that flips the `severity: error` check to failing reaches a terminal error and the commit is blocked.

## Falsifier

Warning blocks commit, OR error doesn't block commit, OR the severity field is declared but unused.

## Proof

Executable proof.

## Notes

Severity is a free-form string today; the runtime partitions on exact-string `warning` (non-blocking) and treats every other value, including the documented `error` and any typo, as blocking. The two-string convention `warning`/`error` is the contract this story exercises; the open `tension:quality-rule-severity-string-footgun` tracks the typo footgun for separate resolution.

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
