# Structural Race Prevention — Design Sketch

**Date:** 2026-08-06
**Status:** Sketch (not a sprint; not authorization to build)

## Idea

Replace race *finding* with race *prevention*. With the race detector
out of every build gate (removed 2026-08, on the grounds that a
probabilistic detector cannot be a deterministic check), the question
"where does race-finding live now?" gets answered: mostly nowhere,
because the codebase adopts conventions under which memory-level data
races are structurally unrepresentable, and the remaining class —
logical races between operations — is already covered by the
deterministic injection-test discipline (`decision:race-injection-hooks`),
which is the only kind `-race` never saw anyway.

The split that makes this tractable: a **data race** is two goroutines
touching the same memory without synchronization — preventable by
construction, because access can be gated by the type system. A
**logical race** is two operations interleaving badly across
transactions or processes (orphan reaper vs. in-flight terminal,
check-then-act across a commit) — not preventable by any wrapper, but
already tested deterministically at each defended seam. Prevention
handles the first class; the existing injection tests keep the second.

## Shape

Four conventions, each mechanically enforceable, adopted as one idiom
sweep per Plumbline's uniformity rule (old idiom out in the same
change, lint so it cannot return):

**1. `Guarded[T]` — the one blessed shared-mutable-state idiom.**
A small generic in `lib/foundation/shared` (or a new
`lib/foundation/guard`):

```go
type Guarded[T any] struct {
    mu sync.Mutex
    v  T          // unexported: unreachable without With
}
func (g *Guarded[T]) With(f func(*T))            { g.mu.Lock(); defer g.mu.Unlock(); f(&g.v) }
func Snapshot[T any](g *Guarded[T]) T            // copy out under the lock
```

The compiler enforces the invariant: no code path reaches `v` without
holding `mu`. Raw `sync.Mutex`/`sync.RWMutex` fields become
lint-forbidden outside the guard package itself (a `forbidigo`/
`depguard`-style rule plus a pin test, same pattern as the proto
comment-hygiene and no-race pins). Today 51 non-test files carry raw
mutexes — that is the sweep's size.

**2. Concurrency budget — `go` is a reviewed event.**
Goroutine creation restricted to a short blessed-package list; every
`go` statement outside it is a lint failure. Today 68 non-test files
spawn goroutines across roughly 15 packages (scheduler, supervisor
dispatch, launch, host agent, proxy, conformance drivers, CLI
watch/compose paths…) — the budget's first job is making that list
explicit and enumerable, its second is stopping casual growth. Most
data races are born in a casually-spawned goroutine capturing loop
state; making spawn sites enumerable shrinks the surface more than any
wrapper.

**3. Immutable crossings.**
Anything sent over a channel or captured by a spawned goroutine is a
value, a deep copy, or a `Guarded[T]` handle — never a shared bare
pointer. Enforced by convention and review rather than fully by lint
(aliasing is not statically decidable in Go); a partial lint can flag
pointer-typed channel elements and pointer captures in `go func`
closures as review triggers.

**4. Persistence-serialized state is the default home.**
State that can live behind the persistence layer's transactions and
advisory locks does; in-process shared state is the exception that
needs a `Guarded[T]` and a reason. This is mostly already true of the
platform's design (claims, queues, parks, leases all serialize through
the store) — the convention names it so new state defaults there.

**What stays as-is:** the injection-test discipline at concurrency
seams (post-commit hooks forcing precise interleavings), which owns
logical races. The sweep does not touch it; if anything, `Guarded[T]`
makes seams easier to instrument.

**Migration shape:** one idiom at a time, not one package at a time —
first land `Guarded[T]` + lint with the guard package exempted,
mechanically convert the 51 mutex files (most are a few lines each),
then land the concurrency-budget lint with the current 15-package
spawn list as the initial budget and burn it down deliberately.

## Open questions

- `RWMutex` semantics: does `Guarded[T]` grow `RWith(func(T))` for
  read paths, or is read/write asymmetry rare enough here to drop?
- Condition-variable and wait patterns (anything currently doing
  `sync.Cond` or unlock-wait-relock) don't fit `With`'s closed scope —
  need a survey of whether rimsky has any, and a second idiom if so.
- Does the budget bless packages or individual functions? Packages are
  enforceable with today's lint stack; functions need a small custom
  analyzer.
- Whether `Snapshot` suffices for cross-goroutine reads or a typed
  read-view idiom is needed for the bigger structures.
- Deadlock risk moves where prevention pushes: single-owner loops and
  nested `With` calls can deadlock without any race. Is a no-nested-
  `With` lint rule (or a lock-ordering convention) part of the same
  sweep?
- Whether the atomics in hot paths (if any exist) get a
  `Guarded`-equivalent or an explicit exemption list.

## Risks / unknowns

- The sweep is large and touches live concurrency: 51 mutex files +
  68 spawn files. Mechanical conversion of correct code can introduce
  bugs where the old locking was subtly load-bearing (lock held across
  a call that must not block, double-lock via nested `With`).
- Closure-scoped access (`With(func(*T))`) fights Go's lack of borrow
  checking: a closure can leak `*T` out by assignment, silently
  defeating the guard. The idiom's guarantee is "unguarded access is
  unrepresentable *without visible smell*", not absolute — the smell
  is at least greppable/lintable.
- A too-small budget list invites cheating (work smuggled into blessed
  packages); a too-large one enforces nothing.
- Performance: `With`'s closure and copy-out snapshots are fine for
  control-plane state but wrong for any hot data path — needs the
  exemption story before the lint is absolute.

## What this is not

- Not a proposal to re-adopt `-race` anywhere, in any gate, at any
  cadence — that question is settled and pinned.
- Not a redesign of the injection-test discipline; logical races stay
  its property.
- Not a general async/architecture rework — the scheduler's and
  supervisor's concurrency structures stay; only their state-access
  idiom and spawn accounting change.
