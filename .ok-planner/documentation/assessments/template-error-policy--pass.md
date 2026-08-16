---
assessment: template-error-policy--pass
subject: story:template-error-policy
way: pass
release: d977250c
outcome: held
warrant: experiment:template-error-policy
---
# Declaring that an error class should not stop the run

The audit drove four templates differing only in the routing action declared for one deterministic executor failure, so the action is the only variable. Under the pass action the run settled fresh while its settling signal still named the error class that was passed — the failure is tolerated, not hidden. The author wrote no handler code to get that.

## Unverified remainder

One error class under one node was exercised. The demonstration does not establish how several declared classes interact when more than one could match a failure.
