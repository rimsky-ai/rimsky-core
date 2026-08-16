---
audit: template-error-policy
artifact: story:template-error-policy
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:06:40Z
checked: 4
unaccounted: 0
---

# All four declared routing actions are honoured at the error site

Supported, with the population enumerated: all four actions the story names were
driven and honoured, and none is unaccounted for. Measured against a container of
the released all-in-one image on four templates that differ only in the routing
action declared for one deterministic executor failure, so the action is the only
variable. Ten checks, none failing. Under pass the run settled fresh while its
settling signal still named the error class that was passed. Under give-up the
run settled failed. Under retry with a declared cap of two, exactly two retries
were taken and signalled, no third was, and the run settled failed once the budget
was spent. Under release-and-requeue each failure emitted its own signal and the
run was dispatched again, settling neither fresh nor failed — it went back for
another attempt, which is what the action names. No handler code was written for
any of the four.
