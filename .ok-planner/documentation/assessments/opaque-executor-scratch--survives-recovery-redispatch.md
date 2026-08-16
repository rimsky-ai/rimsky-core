---
assessment: opaque-executor-scratch--survives-recovery-redispatch
subject: story:opaque-executor-scratch
way: survives-recovery-redispatch
release: d977250c
outcome: held
warrant: experiment:opaque-executor-scratch
---
# Opaque bytes attached to a settling outcome come back on the recovery dispatch

The audit used a third-party executor built for the run speaking the public executor protocol (`catalog:grpc-rpcs/Executor.Execute`, `catalog:grpc-rpcs/ExecutorObservability.Capabilities`), attaching three distinct byte strings containing non-UTF-8 bytes and reporting back only the digest and length of whatever a later dispatch hands it. All three recovery paths carried the bytes: a park's resume, an error's retry, and a stale recovery of a dispatch the runtime reaped for quiet. Each was a second or third dispatch of the same node-run, each was stamped with its prior disposition, and each received back the exact length and digest of what its own earlier dispatch had attached — including the stale-recovery leg, which read bytes attached two dispatches earlier. A dispatch with no predecessor carried none, so the bytes are carried rather than invented. Ten checks across this way and its sibling, none failing.

## Unverified remainder

Three recovery paths were driven with byte strings of a few dozen bytes each. The way does not establish behaviour for very large attachments, nor across a deployment restart between dispatches.
