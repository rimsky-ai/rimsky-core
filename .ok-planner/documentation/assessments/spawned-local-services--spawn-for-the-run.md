---
assessment: spawned-local-services--spawn-for-the-run
subject: story:spawned-local-services
way: spawn-for-the-run
release: d977250c
outcome: held
warrant: experiment:spawned-local-services
---
# A binary named on the command line serves the run, with nothing installed

The audit drove the whole way as one `catalog:cli-verbs/rimsky run` invocation carrying `catalog:cli-flags/--service`, in a scrubbed process environment with no rimsky variables, no container runtime, no compose stack, no service installed on the machine and no hand-written configuration. No process was running the named binary beforehand. The command exited successfully, and its transcript shows the binary announcing itself, serving the node, and both of the instance's nodes reaching terminal success. The same template run without naming the binary did not run at all — the command exited non-zero at registration with a validation failure — so the binding on the command line is what supplies the service. A consumer project can therefore ship a binary and a one-line wrapper instead of an installer.

## Unverified remainder

One binary bound for one run was exercised. The demonstration does not establish behaviour when several binaries are bound in one command, or when the named binary fails to start.
