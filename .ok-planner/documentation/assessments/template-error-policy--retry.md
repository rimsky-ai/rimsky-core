---
assessment: template-error-policy--retry
subject: story:template-error-policy
way: retry
release: d977250c
outcome: held
warrant: experiment:template-error-policy
---
# Declaring a bounded number of retries for an error class

Under the retry action with a declared cap of two, the audit observed exactly two retries taken and signalled, no third, and the run settled failed once the budget was spent. The cap is therefore honoured as a bound rather than as advice, and the attempts are visible in the ledger as they happen.

## Unverified remainder

One cap value on one deterministic failure was exercised. The demonstration does not establish a retry that eventually succeeds within its budget.
