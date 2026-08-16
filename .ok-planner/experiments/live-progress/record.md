---
experiment: live-progress
commit: PENDING
---

# Per-node lifecycle arriving while the run is still in flight

## What it ran against

One `rimsky compose run` invocation of the CLI built from this tree, over a
manifest with two instances: `quick` runs the bundled `verifier-shape-checks`
executor and settles immediately, `lagging` runs the bundled `http-node`
executor against a local server that sleeps eight seconds. The CLI's progress
stream is piped through a reader that stamps each line with the wall-clock
second it arrived. No docker; the run is the CLI's own self-hosted stack in a
scratch home.

## What was observed

Four checks, none failing. Progress lines arrived spread across the run rather
than batched at exit:

    +0s  instance live-demo/lagging: tracking
    +0s  instance live-demo/quick: tracking
    +1s  instance live-demo/lagging node ...: success (terminal/success)
    +1s  instance live-demo/quick node ...: success (terminal/success)
    +1s  instance live-demo/quick node ...: success (terminal/success)
    +2s  instance live-demo/quick: success (nodes=2)
    +11s instance live-demo/lagging node ...: success (terminal/success)
    +11s instance live-demo/lagging: success (nodes=2)
    +11s compose run: all-success (2 instances)

The quick instance's per-node outcome was on screen at +1s and its instance
summary at +2s, nine seconds before the command returned, while the lagging
fetch was still outstanding. A watcher therefore sees which work has settled
and which is still running at any moment, which is the difference between a
healthy run and a hang.

RESULT: PASS
