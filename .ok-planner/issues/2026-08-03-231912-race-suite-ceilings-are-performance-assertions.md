---
issue: race-suite-ceilings-are-performance-assertions
kind: human
category: test-infrastructure
artifacts:
  - decision:test-wallclock-lint-ratchet
  - decision:sqlite-modernc-pure-go
  - decision:sqlite-multiproc-safety
status: open
opened: 2026-08-03T23:19:12Z
---

# The `-race` test targets fail on a slow machine, because their timeouts are sized as performance assertions rather than hang backstops

Two Makefile targets cannot pass on the machine they were run on, and
neither failure indicates a defect in the code they cover.

`make test-foundation` runs `-timeout 180s -race` over the Postgres and
SQLite persistence packages. The SQLite package alone does not finish
within 300s under `-race`. `make test-root` runs `-timeout 180s -race`
over `./lib/runtime/...` and `./lib/graph/scheduler/...`;
`lib/runtime/hostagent` alone takes about 105 seconds there, so the
ceiling is blown once the sibling packages run alongside it.

Both reproduce identically at a commit predating the work that surfaced
them, checked out in a clean worktree, so neither is a regression.

Nothing is deadlocked. A goroutine dump taken at the SQLite timeout
shows 29 goroutines, 24 of them parked in `t.Parallel()` waiting for a
slot, and no mutex, IO, or syscall wait anywhere. The suite also makes
steady progress against a larger ceiling — 7 tests complete in 60
seconds, 13 in 150 — so it is slow, not stuck.

## Why the SQLite package in particular

The project's SQLite driver is pure Go and CGO-free. The entire storage
engine is therefore Go code that the race detector instruments on every
memory access. The Postgres half of the same target does its heavy
lifting in a separate process the detector cannot see, so it barely
notices `-race`. The cost falls on the pure-Go driver specifically, and
it is a consequence of that deliberate choice rather than a defect.

Instrumentation cannot be narrowed. `-race` compiles the whole binary,
dependencies included, and offers no per-module opt-out; the `GORACE`
knobs filter reports, not instrumentation, so suppressing the
dependency would cost the same and only hide output.

Multiplying that cost is per-test setup: the package has 48 test
functions and 28 migrations, and every test that needs a database opens
its own file in its own temporary directory. How many of the 48 pay the
full migration chain has not been measured — only 12 call the migrator
directly, and the rest reach it through a helper that was not traced.
That measurement is the first thing any fix should establish.

## What `-race` does and does not buy here

It is an in-process, Go-memory instrument: it flags two goroutines
touching the same variable without a happens-before edge. It cannot
observe file locks, a second process, or an immediate-mode transaction
losing to another connection — so it contributes nothing toward the
cross-process safety this project commits to for SQLite. What it does
cover is rimsky's own concurrency around the driver, which is worth
covering, and the third-party engine's internals, which is where the
time goes and which a failure would surface anyway.

Static analysis cannot substitute. Deciding the general property
requires resolving aliasing, interface dispatch, channel topology and
goroutine lifetimes across a whole program; Go has no production-grade
static race detector, and the configured linters cover only narrow
adjacent shapes (copied mutexes, loop-variable capture). The detector
is not a proof either — it reports only races on interleavings a test
actually executed.

## The underlying design fault

A single `-timeout` value is being asked to do two incompatible jobs:
detect a genuine block, and bound how long slow-but-correct work may
take. The first wants a small number and the second a large one, so any
single value is a compromise that fails on a slow machine.

The project already separates these correctly *inside* a test — block
on a signal, never on a duration, and let the suite-level timeout be a
harness-layer failsafe rather than a verdict input. The Makefile does
not follow its own rule: a 180-second ceiling on a target needing more
than 300 is a performance assertion wearing a backstop's clothes.

## Options

- Raise the `-race` ceilings to genuine failsafe magnitude (tens of
  minutes), so they fire only on a real hang, and separately reduce the
  per-test setup cost — for instance by copying a pre-migrated template
  database per test instead of re-running the DDL chain. Cost: the two
  changes are independent and the second needs the measurement above
  before it can be scoped.
- Narrow which packages run under `-race` to those whose concurrency is
  rimsky's own. Cost: does not separate cleanly here, since the SQLite
  package is rimsky code that imports the engine, so its instrumentation
  comes along with any test of the queue or the advisory locker.
- Tune the ceilings to current machine speed. Cost: re-creates the same
  failure on the next slower machine, and is the exact move the
  project's own testing rule forbids.

The ruling decides whether the ceilings become failsafes and the setup
cost is attacked on its own merits, or the `-race` coverage is narrowed
instead.
