---
assessment: spawned-local-services--gone-when-the-run-returns
subject: story:spawned-local-services
way: gone-when-the-run-returns
release: d977250c
outcome: held
warrant: experiment:spawned-local-services
---
# The spawned service disappears with the run that needed it

When the command returned, the audit found the process that served the run gone, no process anywhere running the bound binary, and nothing left over from the command itself. A second invocation of the same command was served by a different process, which was likewise gone when it returned. Nothing therefore accumulates on the developer's machine between runs — there is no daemon to stop and no state left behind to clean up.

## Unverified remainder

Two successive invocations were exercised. The demonstration does not establish what is left behind when the command is interrupted rather than allowed to return.
