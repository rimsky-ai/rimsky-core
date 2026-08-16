---
audit: termination
artifact: decision:termination
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# What the run-to-terminal verbs actually wait for before exiting

Unsupported: the verbs do wait for all work to finish rather than exiting at submission, but the gate the choice names does not exist. Nothing in the platform promotes an instance to a terminal state on its own — the terminated stamp on an instance row is written only by the explicit administrative terminate route, and no scheduler, supervisor, or reaper path sets it — so there is no instance-terminal promotion for a verb to wait on. What the one-shot verbs actually do is poll a client-side quiescence check per instance: no frames in the running state and no pending messages on its queue. When every declared instance passes that check they call the terminate route themselves with a workflow-complete reason, and only then exit, so the instance becomes terminal because the verb made it so rather than the other way round. The remote ephemeral run's cleanup path takes the choice at its word and polls the instance until its terminated stamp appears, which nothing but an external terminate will ever produce, so that loop is bounded only by the opt-in timeout. What does hold is the park half of the rationale: a parked node-run holds its frame open, so quiescence is not reached while a park is outstanding, the verbs wait for the supervisor's time-wake, and no verb-level park handling exists anywhere in the one-shot code. The rejected exit-at-submission shape is likewise absent.
