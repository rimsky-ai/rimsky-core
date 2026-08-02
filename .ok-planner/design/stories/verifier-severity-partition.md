---
story: verifier-severity-partition
---

# Template author distinguishes warning vs error

## Story

As a template author declaring data-quality checks, I can label a check with the warning or error severity and have the verifier honor the partition — failing-warning is non-blocking, failing-error blocks the commit — so that I distinguish observed-but-tolerated quality issues from blocking ones.
