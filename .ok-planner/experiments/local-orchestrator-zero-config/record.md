---
experiment: local-orchestrator-zero-config
commit: d977250c
---

# Ad-hoc template run with one binary, one command, and no configuration

## What it ran against

The `rimsky` CLI binary built from this tree, invoked as `rimsky run <template>`
inside a scrubbed process environment (`env -i`, an empty `HOME`, no rimsky
variables) and a fresh working directory. No docker, no compose stack, no
external executor process. Two templates ship with the experiment: both name
the bundled `verifier-shape-checks` executor, one with clean rows and one with
a null in the checked field.

## What was observed

Both runs booted an in-process stack, migrated a fresh SQLite database under
the working directory, registered and deployed the template, created and woke
an instance, and drove it to terminal before returning. The transcript records
`bundled executor registered in-process` for `http-node`, `verifier-http`, and
`verifier-shape-checks`; the claude-agent executor and both claim producers
were skipped as unconfigured, which cost nothing because the template does not
name them.

The clean-rows template exited 0 with both nodes at `terminal/success`. The
null-row template exited 1 with the verify node at
`terminal/error/verifier/check_failed/no_nulls` — the bundled verifier's own
error class, produced by its own check logic, which is what separates a real
service from a stub.

RESULT: PASS
