---
experiment: operator-onboarding
commit: PENDING
---

# A newcomer copies the shipped example and runs the dev loop

## What it ran against

`way-copy-and-run.sh` checks what the tree gives a newcomer to copy, then copies
the shipped onboarding template and its walkthrough script out of the tree into a
scratch directory and drives them against a `rimsky-all-in-one` container booted
at `RIMSKY_IMAGE_TAG`. It uses the CLI binary built from this tree.

## What was observed

The tree ships the onboarding template and the walkthrough script, and the
README's first-steps walkthrough names the template by path. Against the copy in
the scratch directory, the single run verb exited 0 and printed
`instance_id=<uuid>`, the watch verb returned 0 once the instance reached a
terminal state, and the instance's node reported `terminal/success`. Running the
copied walkthrough script — which wraps the same two verbs — exited 0 and printed
its dev-loop-complete line.

Eleven checks, none failing.

RESULT: PASS
