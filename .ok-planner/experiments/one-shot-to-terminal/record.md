---
experiment: one-shot-to-terminal
commit: d977250c
---

# A compose manifest driven to terminal by the invocation that started it

## What it ran against

The `rimsky` CLI binary built from this tree, invoked as
`rimsky compose run rimsky-compose.yml` in a scrubbed environment with an empty
`HOME`, so no rimsky is running beforehand and none could be addressed. The
manifest declares two templates and two instances against the bundled
`verifier-shape-checks` executor: one instance whose check passes, one whose
check fails.

## What was observed

The single invocation stood the stack up, applied the manifest, woke both
instances, and reported each reaching terminal — `alpha: success (nodes=2)` and
`beta: failure (nodes=2)` — before returning, in about two seconds. The run
exited 1, the mixed-outcome class.

Afterwards the control-api port the run had allocated (read out of its own
transcript) refused connections, so the invocation left nothing behind for the
operator to tear down.

Six checks, none failing.

RESULT: PASS
