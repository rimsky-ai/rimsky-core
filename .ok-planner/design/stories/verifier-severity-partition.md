---
story: verifier-severity-partition
status: as-is
---

# Template author distinguishes warning vs error

## Story

As a template author declaring data-quality checks, I can label a check with the warning or error severity and have the verifier honor the partition — failing-warning is non-blocking (the run still succeeds), failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.

Severity partition in verifier nodes: the warning severity is non-blocking; the error severity is blocking. Both observable through the runtime surface. Severity is validated against a closed allowlist — empty defaults to error, otherwise the value must be exactly warning or error; the verifier rejects any other value, including a typo, with a structured error before running any check, rather than silently treating it as blocking.

Template authors distinguish observed-but-tolerated quality issues from blocking ones without re-architecting their pipeline; runtime honors the partition consistently.
