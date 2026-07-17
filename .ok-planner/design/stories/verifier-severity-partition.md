---
story: verifier-severity-partition
status: as-is
---

# Template author distinguishes warning vs error

## Role

As a template author declaring data-quality checks, I can label a check with the warning or error severity and have the verifier honor the partition — failing-warning is non-blocking (the run still succeeds), failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.

## Capability

Severity partition in verifier nodes: the warning severity is non-blocking; the error severity is blocking. Both observable through the runtime surface. Severity is validated against a closed allowlist — empty defaults to error, otherwise the value must be exactly warning or error; the verifier rejects any other value, including a typo, with a structured error before running any check, rather than silently treating it as blocking.

## Business value

Template authors distinguish observed-but-tolerated quality issues from blocking ones without re-architecting their pipeline; runtime honors the partition consistently.

## Acceptance

With a template whose verifier node carries one warning-severity failing check and one error-severity passing check against an in-bounds dataset, the dispatch reaches terminal success and the observability surface records the failed check as warning. A second dispatch against an out-of-bounds dataset that flips the error-severity check to failing reaches a terminal error and the commit is blocked.

## Falsifier

Warning blocks commit, OR error doesn't block commit, OR the severity field is declared but unused.

## Proof

Executable proof.
