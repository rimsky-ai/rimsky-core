---
issue: suite-verdict-is-load-dependent
kind: human
category: determinism
artifacts: []
status: open
opened: 2026-08-07T22:47:41Z
---

# The test suite's verdict depends on how busy the machine is

`rules.md` commits the suite to a verdict that is a function of the code alone.
It is not. On a machine running other projects' containers, the scenario suite
fails; on a quiet machine the same tree passes. Five runs across two trees
produced five different failure sets, and the tree with no changes at all failed
too.

Two independent mechanisms produce this, and they need different fixes.

## Tests that read state written asynchronously

Some tests perform an action and then assert on its result on the next line,
with no wait. The result is written asynchronously, so the assertion races the
write. `TestUnresolvedClaimProducerFailsDispatchLoudly` calls `runtime.RunNode`
and immediately reads the node's latest run, expecting `failed`; under load it
observes `stale` and reports a mismatch. rimsky was correct — the test looked
too early.

This is the pattern `rules.md` already forbids ("Synchronize on the event, not
on wall-clock time"), and the harness already ships the primitive these
assertions should use: `test/support/scenario/harness.go::Harness.WaitForNodeState`
blocks until the state appears. The remedy is mechanical — move every
read-after-action assertion onto a blocking wait. No judgment needed; noted here
because it shares a root with the second mechanism and should be fixed in the
same pass.

## The hang backstop is an aggregate wall-clock budget

`rules.md` bans wall-clock constants from the pass/fail path — "any finite value
is an unprovable guess about the load ceiling" — then exempts one, asserting the
suite-level `go test -timeout` is "load-independent in outcome" and that "load
changes how long a pass takes, never whether it passes."

That assertion is false, and the reason is structural rather than a matter of
picking a better number. Go's `-timeout` is a **per-binary aggregate** budget:
one ceiling covers every test in the package, so it must be sized to total
runtime, which scales with both test count and machine load. A single test
blocking longer under load consumes the budget belonging to every other test in
the package — which is why each run killed a different, arbitrary set.

The headroom is thinner than the observed variance. The scenarios package runs
154–197s on a quiet machine; `make test-root` allows 300s; under contention it
exceeded 600s and was killed twice, with `panic: test timed out after 10m0s`.

`tools/gotest-guard.sh` closes half of this. It detects the kill and states
loudly that the results are incomplete — but it exits 1 with the same message
whether the cause was a saturated daemon or a genuine hang, and advises the
reader to "raise the -timeout for this target or fix the slow/hanging tests."
So the constraint is detected and then reported as a verdict anyway.

## Options

- **Make the backstop measure progress rather than elapsed time.** Run with
  `-timeout 0` and move hang detection into the guard: consume `go test -json`
  and kill only when no test event has arrived for some interval. A hung test
  emits nothing and completes nothing; a slow test keeps completing tests, just
  later. Load stops being a verdict input entirely, and a genuine hang still
  dies loudly. Costs a real guard implementation, and the no-progress interval
  is itself a constant — but one that a correct suite never approaches at any
  load, rather than one sized to total runtime.
- **Throttle concurrency** to fit the machine, as `test-services` and
  `test-examples` already do with `-p 2 -parallel 4`. Makes runs more
  predictable and is worth doing on its own merits; narrows the window rather
  than closing it, since the aggregate ceiling remains.
- **Classify saturation separately from failure.** Have the guard distinguish
  "the environment was saturated" from "a test hung" and report the former as an
  inconclusive run rather than naming arbitrary tests. Honest, and it does not
  make the suite runnable under load — it only stops the run from lying about
  why it stopped.
- **Halt and wait for resources** before starting, refusing to run when the
  daemon is already loaded. Avoids producing a misleading verdict at all; turns
  a busy machine into a blocked developer.
- **Raise the ceilings.** Costs nothing to try and settles nothing: it lengthens
  the fuse, which is the move `rules.md` already names as the tell that a
  wall-clock constant is doing verdict work.

Whichever way it goes, `rules.md`'s claim that the suite-level timeout is
load-independent needs to change, since it is the sentence that licensed the
current arrangement.

## Provenance

Found while re-running the suite to check whether a batch of repairs had caused
regressions (they had not). Evidence: five runs of
`go test ./test/scenarios/... ./lib/foundation/persistence/...` — three on a tree
with uncommitted repairs, one on a pristine `HEAD` worktree, one instrumented
with per-second Docker sampling. Docker container count rose 36 → 67 during a
run and memory peaked at 82% of the daemon's 7.75 GiB, so memory was not
exhausted; two of the five runs were killed by the package timeout, and the
others failed fast assertions with no timeout at all. All four suspect tests
pass three consecutive times when run in isolation on the same tree.
