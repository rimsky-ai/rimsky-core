---
assessment: one-shot-to-terminal--drive-to-terminal
subject: story:one-shot-to-terminal
way: drive-to-terminal
release: d977250c
outcome: held
warrant: experiment:one-shot-to-terminal
---
# One invocation stands the run up and finishes it

The audit drove a compose manifest (`catalog:compose-manifest-keys/project`) declaring two templates and two instances through `catalog:cli-verbs/rimsky compose run`, in a scrubbed environment with an empty home directory — so no deployment was running beforehand and none could have been addressed even if one had been. The single invocation stood the stack up, applied the manifest, and reported both declared instances reaching a terminal state before it returned. One instance succeeded and one failed, which also shows the invocation waits for real outcomes rather than for dispatch. Six checks across this way and its sibling, none failing.

## Unverified remainder

Two instances across two templates were declared. The way does not establish behaviour for a manifest with many instances, or one whose instances never reach a terminal state.
