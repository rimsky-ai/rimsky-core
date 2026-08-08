---
issue: suite-verdict-is-load-dependent
kind: human
category: determinism
artifacts:
  - decision:testing-scenario-based-e2e
  - decision:test-wallclock-lint-ratchet
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T22:47:41Z
---

# The test suite's verdict depends on how busy the machine is

The project commits, in writing, to a test suite whose pass/fail is a function of
the code alone. It is not. Running other projects' containers on the same machine
makes the suite fail; a quiet machine passes the identical tree. Five runs across
two trees produced five different failure sets, and the tree with no changes at
all failed too — which is how this was found, while checking whether a batch of
repairs had caused regressions. They had not.

Two independent mechanisms produce it. One is a straightforward defect with a
known fix. The other rests on a claim the project has written into a live design
decision, and that claim is false.

## Tests that read state written asynchronously

Several tests perform an action and assert on its result on the very next line,
with no wait. The result is written asynchronously, so the assertion races the
write. `TestUnresolvedClaimProducerFailsDispatchLoudly` calls `runtime.RunNode`
and immediately reads the node's latest run expecting `failed`; under load it
observes `stale` and reports a mismatch. rimsky was correct — the test looked too
early.

This is not one test. Of the seven scenario files that drive `runtime.RunNode`
directly, six read run state immediately afterward with no blocking wait; only
one uses the harness's own `WaitForNodeState`, which blocks until the state
appears (`test/support/scenario/harness.go`). The remedy is mechanical, and the
project's testing rules already forbid the pattern — synchronize on the event,
not on wall-clock time. No judgment is needed here; it is named because it shares
a root with the second mechanism and belongs in the same pass.

## The hang backstop is an aggregate wall-clock budget

The project bans wall-clock constants from the pass/fail path — "any finite value
is an unprovable guess about the load ceiling" — then exempts exactly one: Go's
suite-level `-timeout`, asserted to be load-independent in outcome, on the
grounds that load changes how long a pass takes but never whether it passes.

That assertion is false, and structurally so rather than for want of a better
number. Go's `-timeout` is a **per-package aggregate** budget: one ceiling covers
every test in the package, so it must be sized to total runtime, which scales
with both test count and machine load. One test blocking longer under load
consumes budget belonging to every other test in the package — which is why each
run killed a different, arbitrary set.

The headroom is thinner than the observed variance. The scenarios package runs
154–197s on a quiet machine and `make test-root` allows 300s; under contention it
exceeded 600s and was killed twice, with `panic: test timed out after 10m0s`.

The falsified claim is load-bearing in two places. The project's own testing
rules state it, and so does the design decision governing scenario testing
(`decision:testing-scenario-based-e2e`), whose rationale for unbounded wait
helpers is precisely that the suite-level timeout keeps the verdict
load-independent. Whichever way this is ruled, that decision changes with it.

## What already exists

The wall-clock lint (`tools/wallclock-lint/`) scans Go sources for four in-test
polling idioms — `Eventually`, `select` on a timer, and two deadline loops. It
has no detector for `-timeout` anywhere, in the Makefile or otherwise, so the
aggregate backstop is outside its reach by construction.

The test guard (`tools/gotest-guard.sh`) closes half the reporting gap. It
detects the kill and states loudly that the results are incomplete — but exits
with the same message whether the cause was a saturated daemon or a genuine hang,
and advises the reader to raise the timeout or fix the slow tests. The constraint
is detected and then reported as a verdict anyway.

Machinery for the strongest option is already in the tree: `make test-report`
runs `gotestsum --jsonfile` across all four modules and post-processes the
combined JSON. A progress-based watchdog would extend that plumbing rather than
introduce a dependency; it is simply not wired as a gate today.

## Options

- **Make the backstop measure progress rather than elapsed time.** Run with
  `-timeout 0` and move hang detection into the guard: consume the test runner's
  JSON event stream and kill only when no test event has arrived for some
  interval. A hung test emits nothing and completes nothing; a slow test keeps
  completing tests, just later. Load stops being a verdict input, and a genuine
  hang still dies loudly. Costs a real guard implementation, and the no-progress
  interval is itself a constant — but one a correct suite never approaches at any
  load, rather than one sized to total runtime.
- **Throttle concurrency** to fit the machine, as two of the module targets
  already do. Makes runs more predictable and is worth doing on its own merits;
  narrows the window rather than closing it, since the aggregate ceiling remains.
- **Classify saturation separately from failure.** Have the guard distinguish a
  saturated environment from a hung test and report the former as an inconclusive
  run rather than naming arbitrary tests. Honest, and it does not make the suite
  runnable under load — it only stops the run from lying about why it stopped.
- **Halt and wait for resources**, refusing to start when the machine is already
  loaded. Avoids producing a misleading verdict at all; turns a busy machine into
  a blocked developer.
- **Raise the ceilings.** Costs nothing to try and settles nothing: it lengthens
  the fuse, which is the move the project's own rules name as the tell that a
  wall-clock constant is doing verdict work.

The ruling decides what the hang backstop measures, and what the scenario-testing
decision says about it.

## Ruling

> Move the hang backstop from elapsed time
> to progress. Run the suites with no time ceiling at all and let the test guard
> watch the runner's event stream, killing the run only when nothing has
> completed for a good while. Fix the six racing reads in the same pass by
> routing them through the harness's blocking wait — that part is already forced
> by the testing rules and needs no decision. Then correct the scenario-testing
> decision and the project rules: the suite-level timeout is not load-independent,
> and that sentence is what licensed the current arrangement. Throttling
> concurrency on the remaining targets is worth doing alongside, on its own
> merits, not as the fix.
>
> Rationale: the project has already committed to verdicts that are a function of
> the code alone, and spent real effort enforcing it inside tests — a lint that
> bans exactly this idiom per-assertion while the aggregate ceiling does the same
> work per-package is an enforcement gap, not a considered exemption. Of the
> options, only the first actually removes load from the verdict; the middle two
> improve honesty or odds without closing the mechanism, and the last is the move
> the rules themselves name as the tell. The remaining objection — that a
> no-progress interval is still a constant — is answered by what it measures: a
> correct suite emits completions continuously at any load, so the interval never
> binds, whereas an aggregate budget binds precisely when the machine is busy.
>
> One thing to establish while building it: whether the event stream emits
> reliably enough during a single long-running test to separate "slow" from
> "hung". If it does not, the watchdog loses its discriminating power, and the
> honest fallback is to have the guard report saturation as an inconclusive run
> rather than naming arbitrary tests.
