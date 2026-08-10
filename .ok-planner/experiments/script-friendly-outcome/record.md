---
experiment: script-friendly-outcome
commit: PENDING
---

# A script branching on a one-shot run's outcome class

## What it ran against

Three `rimsky compose run` invocations from a shell `case` statement that reads
only `$?` and discards the transcript entirely. Two manifests use the bundled
`verifier-shape-checks` executor (all-pass, and one-pass-one-fail); the third
uses the bundled `http-node` executor against a local server that sleeps 20
seconds, run under `--timeout 3s`.

## What was observed

The three classes came back distinct and in the expected order:

    all-success manifest        exit 0   all succeeded
    one-failure manifest        exit 1   something failed
    slow manifest, --timeout 3s exit 2   the run was bounded out

Every branch was taken on the exit status alone; nothing parsed a log line.

RESULT: PASS
