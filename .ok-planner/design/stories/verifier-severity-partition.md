---
story: verifier-severity-partition
status: as-is
---

# Template author distinguishes warning vs error

## Role

As a template author declaring data-quality checks, I can label a check with the warning or error severity and have the verifier honor the partition — failing-warning is non-blocking (the run still succeeds), failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.

## Capability

Severity partition in verifier nodes: the warning severity is non-blocking; the error severity (and any non-warning string) is blocking. Both observable through the runtime surface. Severity is a free-form string; the runtime partitions on the exact-string warning value (non-blocking) and treats every other value, including the documented error value and any typo, as blocking. The two-string convention (warning and error) is the contract this story exercises; `tension:quality-rule-severity-string-footgun` tracks the typo footgun.

## Business value

Template authors distinguish observed-but-tolerated quality issues from blocking ones without re-architecting their pipeline; runtime honors the partition consistently.

## Acceptance

With a template whose verifier node carries one warning-severity failing check and one error-severity passing check against an in-bounds dataset, the dispatch reaches terminal success and the observability surface records the failed check as warning. A second dispatch against an out-of-bounds dataset that flips the error-severity check to failing reaches a terminal error and the commit is blocked.

## Falsifier

Warning blocks commit, OR error doesn't block commit, OR the severity field is declared but unused.

## Proof

Executable proof.
